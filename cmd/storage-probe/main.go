package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"dorm-memorial/internal/storage/alist"
)

func main() {
	filePath := flag.String("file", "", "local file used for the upload probe")
	remotePath := flag.String("remote", "", "object path below ALIST_ROOT")
	keep := flag.Bool("keep", false, "keep the uploaded probe object")
	flag.Parse()

	baseURL := strings.TrimSpace(os.Getenv("ALIST_BASE_URL"))
	if baseURL == "" {
		log.Fatal("ALIST_BASE_URL is required")
	}
	client, err := alist.New(alist.Config{
		BaseURL: baseURL,
		Token:   os.Getenv("ALIST_TOKEN"),
		Root:    envOr("ALIST_ROOT", "/dorm-memorial/probe"),
	})
	if err != nil {
		log.Fatal(err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	started := time.Now()
	if err := client.Put(ctx, *remotePath, source, size); err != nil {
		log.Fatalf("upload failed: %v", err)
	}
	info, err := client.Stat(ctx, *remotePath)
	if err != nil {
		log.Fatalf("stat failed: %v", err)
	}
	if info.Size != size {
		log.Fatalf("size mismatch: local=%d remote=%d", size, info.Size)
	}

	remote, err := client.Open(ctx, *remotePath)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}
	remoteHash, err := hashReader(remote)
	remote.Close()
	if err != nil {
		log.Fatalf("hash remote object: %v", err)
	}
	if remoteHash != localHash {
		log.Fatalf("hash mismatch: local=%s remote=%s", localHash, remoteHash)
	}
	if err := client.Move(ctx, *remotePath, movedPath); err != nil {
		log.Fatalf("move failed: %v", err)
	}
	if !*keep {
		if err := client.Delete(ctx, movedPath); err != nil {
			log.Fatalf("cleanup failed: %v", err)
		}
	}

	fmt.Printf("storage probe passed: bytes=%d sha256=%s elapsed=%s kept=%t\n", size, localHash, time.Since(started).Round(time.Millisecond), *keep)
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
