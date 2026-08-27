package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dorm-memorial/internal/storage"
	"dorm-memorial/internal/storage/alist"
)

func main() {
	filePath := flag.String("file", "", "local file used for the upload probe")
	remotePath := flag.String("remote", "", "object path below ALIST_ROOT")
	keep := flag.Bool("keep", false, "keep the uploaded probe object")
	skipMove := flag.Bool("skip-move", false, "skip move/rename verification for providers that do not support file rename")
	timeout := flag.Duration("timeout", 3*time.Hour, "maximum duration of the remote probe")
	flag.Parse()

	baseURL := strings.TrimSpace(os.Getenv("ALIST_BASE_URL"))
	if baseURL == "" {
		log.Fatal("ALIST_BASE_URL is required")
	}
	client, err := alist.New(alist.Config{
		BaseURL:  baseURL,
		Token:    os.Getenv("ALIST_TOKEN"),
		Username: os.Getenv("ALIST_USERNAME"),
		Password: os.Getenv("ALIST_PASSWORD"),
		Root:     envOr("ALIST_ROOT", "/dorm-memorial/probe"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if os.Getenv("ALIST_USERNAME") != "" || os.Getenv("ALIST_PASSWORD") != "" {
		authCtx, authCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Authenticate(authCtx)
		authCancel()
		if err != nil {
			log.Fatalf("authenticate dedicated AList user: %v", err)
		}
	}

	source, size, name, localHash, err := openSource(*filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer source.Close()
	if *filePath == "" {
		defer os.Remove(source.Name())
	}
	if *remotePath == "" {
		*remotePath = "/" + time.Now().UTC().Format("20060102T150405Z") + "-" + name
	}
	movedPath := pathpkg.Join(pathpkg.Dir(*remotePath), "verified-"+pathpkg.Base(*remotePath))

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := runProbe(ctx, client, source, name, *remotePath, movedPath, size, localHash, *keep, *skipMove)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(
		"storage probe passed: bytes=%d sha256=%s upload=%s upload_mib_s=%.2f range=%s download=%s download_mib_s=%.2f peak_heap_mib=%.2f peak_sys_mib=%.2f elapsed=%s kept=%t\n",
		size,
		localHash,
		result.uploadElapsed.Round(time.Millisecond),
		transferRate(size, result.uploadElapsed),
		result.rangeElapsed.Round(time.Millisecond),
		result.downloadElapsed.Round(time.Millisecond),
		transferRate(size, result.downloadElapsed),
		bytesToMiB(result.memory.heapAlloc),
		bytesToMiB(result.memory.sys),
		result.elapsed.Round(time.Millisecond),
		*keep,
	)
}

type probeResult struct {
	uploadElapsed   time.Duration
	rangeElapsed    time.Duration
	downloadElapsed time.Duration
	elapsed         time.Duration
	memory          memoryPeaks
}

func runProbe(ctx context.Context, client *alist.Client, source *os.File, name, remotePath, movedPath string, size int64, localHash string, keep, skipMove bool) (result probeResult, err error) {
	started := time.Now()
	stopMemorySampler := startMemorySampler()
	memoryStopped := false
	defer func() {
		if !memoryStopped {
			stopMemorySampler()
		}
	}()

	remoteMayExist := false
	defer func() {
		if err == nil || keep || !remoteMayExist {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, candidate := range []string{remotePath, movedPath} {
			if cleanupErr := client.Delete(cleanupCtx, candidate); cleanupErr == nil {
				log.Printf("failure cleanup removed: path=%q", candidate)
			}
		}
	}()

	log.Printf("upload started: name=%q bytes=%d", name, size)
	uploadStarted := time.Now()
	if err := client.Put(ctx, remotePath, source, size); err != nil {
		return result, fmt.Errorf("upload failed: %w", err)
	}
	remoteMayExist = true
	uploadElapsed := time.Since(uploadStarted)
	log.Printf("upload completed: elapsed=%s rate=%.2f MiB/s", uploadElapsed.Round(time.Millisecond), transferRate(size, uploadElapsed))

	info, err := waitForStat(ctx, client, remotePath)
	if err != nil {
		return result, fmt.Errorf("stat failed after consistency wait: %w", err)
	}
	if info.Size != size {
		return result, fmt.Errorf("size mismatch: local=%d remote=%d", size, info.Size)
	}

	rangeElapsed, err := verifyRange(ctx, client, source, remotePath, size)
	if err != nil {
		return result, fmt.Errorf("range verification failed: %w", err)
	}
	log.Printf("range verification completed: elapsed=%s", rangeElapsed.Round(time.Millisecond))

	log.Printf("download verification started")
	downloadStarted := time.Now()
	remote, err := client.Open(ctx, remotePath)
	if err != nil {
		return result, fmt.Errorf("download failed: %w", err)
	}
	remoteHash, err := hashReader(remote)
	remote.Close()
	if err != nil {
		return result, fmt.Errorf("hash remote object: %w", err)
	}
	if remoteHash != localHash {
		return result, fmt.Errorf("hash mismatch: local=%s remote=%s", localHash, remoteHash)
	}
	downloadElapsed := time.Since(downloadStarted)
	log.Printf("download verification completed: elapsed=%s rate=%.2f MiB/s", downloadElapsed.Round(time.Millisecond), transferRate(size, downloadElapsed))

	cleanupPath := movedPath
	if skipMove {
		cleanupPath = remotePath
	} else if err := client.Move(ctx, remotePath, movedPath); err != nil {
		return result, fmt.Errorf("move failed: %w", err)
	}
	if !keep {
		if err := client.Delete(ctx, cleanupPath); err != nil {
			return result, fmt.Errorf("cleanup failed: %w", err)
		}
		remoteMayExist = false
	}

	peaks := stopMemorySampler()
	memoryStopped = true
	return probeResult{
		uploadElapsed:   uploadElapsed,
		rangeElapsed:    rangeElapsed,
		downloadElapsed: downloadElapsed,
		elapsed:         time.Since(started),
		memory:          peaks,
	}, nil
}

type memoryPeaks struct {
	heapAlloc uint64
	sys       uint64
}

func startMemorySampler() func() memoryPeaks {
	stop := make(chan struct{})
	done := make(chan memoryPeaks, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var peaks memoryPeaks
		for {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if stats.HeapAlloc > peaks.heapAlloc {
				peaks.heapAlloc = stats.HeapAlloc
			}
			if stats.Sys > peaks.sys {
				peaks.sys = stats.Sys
			}
			select {
			case <-stop:
				done <- peaks
				return
			case <-ticker.C:
			}
		}
	}()
	return func() memoryPeaks {
		close(stop)
		return <-done
	}
}

func verifyRange(ctx context.Context, client *alist.Client, source *os.File, objectPath string, size int64) (time.Duration, error) {
	const maximumPrefix = 1024
	prefixSize := min(size, maximumPrefix)
	if prefixSize == 0 {
		return 0, nil
	}
	expected := make([]byte, prefixSize)
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek local source: %w", err)
	}
	if _, err := io.ReadFull(source, expected); err != nil {
		return 0, fmt.Errorf("read local prefix: %w", err)
	}

	started := time.Now()
	var resp *http.Response
	var err error
	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute}
	for attempt, delay := range delays {
		if delay > 0 {
			log.Printf("range not ready; retrying in %s", delay)
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(delay):
			}
		}
		resp, err = client.OpenRange(ctx, objectPath, fmt.Sprintf("bytes=0-%d", prefixSize-1))
		if err == nil || !strings.Contains(err.Error(), "HTTP 416") || attempt == len(delays)-1 {
			break
		}
	}
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("expected HTTP 206, got %d", resp.StatusCode)
	}
	actual, err := io.ReadAll(io.LimitReader(resp.Body, prefixSize+1))
	if err != nil {
		return 0, fmt.Errorf("read remote range: %w", err)
	}
	if !bytes.Equal(actual, expected) {
		return 0, fmt.Errorf("remote prefix differs from local file")
	}
	return time.Since(started), nil
}

func waitForStat(ctx context.Context, client *alist.Client, objectPath string) (storage.ObjectInfo, error) {
	delays := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second, 30 * time.Second}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			log.Printf("object not visible yet; retrying stat in %s", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return storage.ObjectInfo{}, ctx.Err()
			case <-timer.C:
			}
		}
		if err := client.RefreshDirectory(ctx, pathpkg.Dir(objectPath)); err != nil {
			lastErr = err
			continue
		}
		info, err := client.Stat(ctx, objectPath)
		if err == nil {
			return info, nil
		}
		lastErr = err
	}
	return storage.ObjectInfo{}, lastErr
}

func transferRate(size int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return bytesToMiB(uint64(size)) / elapsed.Seconds()
}

func bytesToMiB(value uint64) float64 {
	return float64(value) / (1024 * 1024)
}

func openSource(filePath string) (*os.File, int64, string, string, error) {
	if filePath == "" {
		temp, err := os.CreateTemp("", "dorm-memorial-probe-*.txt")
		if err != nil {
			return nil, 0, "", "", err
		}
		content := []byte("dorm memorial storage probe\n")
		if _, err := temp.Write(content); err != nil {
			temp.Close()
			return nil, 0, "", "", err
		}
		if _, err := temp.Seek(0, io.SeekStart); err != nil {
			temp.Close()
			return nil, 0, "", "", err
		}
		return temp, int64(len(content)), filepath.Base(temp.Name()), hexHash(content), nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, "", "", err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, "", "", err
	}
	hash, err := hashReader(file)
	if err != nil {
		file.Close()
		return nil, 0, "", "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, 0, "", "", err
	}
	return file, info.Size(), filepath.Base(filePath), hash, nil
}

func hashReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hexHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
