package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// Voice notes are intentionally much smaller than general media. Keeping
	// this cap in the media package lets both HTTP admission and direct callers
	// enforce the same limit before staging to disk.
	MaxAudioUploadBytes int64 = 16 << 20
	audioHeaderBytes          = 512
)

// prepareAudioUpload stages audio before it is persisted. Audio is delivered
// inline by the HTTP layer, so accepting an arbitrary body under an audio MIME
// type would let callers store and replay unrelated content indefinitely.
// Container checks stay bounded to the first header and do not decode attacker
// controlled sample data.
func prepareAudioUpload(ctx context.Context, input UploadInput) (UploadInput, func(), error) {
	if err := ctx.Err(); err != nil {
		return UploadInput{}, func() {}, err
	}
	if input.Body == nil || input.Size <= 0 || input.Size > MaxAudioUploadBytes {
		return UploadInput{}, func() {}, ErrInvalid
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(input.MimeType, ";")[0]))
	if !strings.HasPrefix(mimeType, "audio/") {
		return UploadInput{}, func() {}, ErrInvalid
	}
	temp, err := os.CreateTemp("", "dorm-audio-upload-*.part")
	if err != nil {
		return UploadInput{}, func() {}, fmt.Errorf("create audio staging file: %w", errors.Join(ErrStorageUnavailable, err))
	}
	tempPath := temp.Name()
	cleanupTemp := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	written, copyErr := copyUploadBody(ctx, temp, input.Body, input.Size)
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil || written != input.Size {
		cleanupTemp()
		if copyErr != nil && !errors.Is(copyErr, context.Canceled) && !errors.Is(copyErr, context.DeadlineExceeded) {
			return UploadInput{}, func() {}, fmt.Errorf("stage audio upload: %w", errors.Join(ErrInvalid, copyErr))
		}
		return UploadInput{}, func() {}, fmt.Errorf("stage audio upload: %w", errors.Join(ErrInvalid, copyErr, closeErr))
	}
	file, err := os.Open(tempPath)
	if err != nil {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("open staged audio: %w", errors.Join(ErrInvalid, err))
	}
	prefix := make([]byte, audioHeaderBytes)
	n, readErr := file.Read(prefix)
	_ = file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("read audio header: %w", errors.Join(ErrInvalid, readErr))
	}
	if !audioHeaderMatches(mimeType, prefix[:n]) {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("audio content does not match MIME %q: %w", mimeType, ErrInvalid)
	}
	prepared, err := os.Open(tempPath)
	if err != nil {
		cleanupTemp()
		return UploadInput{}, func() {}, fmt.Errorf("reopen staged audio: %w", errors.Join(ErrInvalid, err))
	}
	preparedInput := input
	preparedInput.Body = prepared
	preparedInput.Size = written
	return preparedInput, func() {
		_ = prepared.Close()
		_ = os.Remove(tempPath)
	}, nil
}

func audioHeaderMatches(mimeType string, header []byte) bool {
	if len(header) < 4 {
		return false
	}
	hasBMFF := len(header) >= 8 && bytes.Equal(header[4:8], []byte("ftyp"))
	switch mimeType {
	case "audio/mp4", "audio/x-m4a", "audio/3gpp", "audio/3gpp2":
		return hasBMFF
	case "audio/ogg", "audio/opus", "audio/oga":
		return bytes.HasPrefix(header, []byte("OggS"))
	case "audio/webm", "audio/x-matroska":
		return bytes.HasPrefix(header, []byte{0x1a, 0x45, 0xdf, 0xa3})
	case "audio/mpeg", "audio/mp3":
		return bytes.HasPrefix(header, []byte("ID3")) || hasMPEGFrameSync(header)
	case "audio/wav", "audio/x-wav":
		return len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE"))
	case "audio/flac", "audio/x-flac":
		return bytes.HasPrefix(header, []byte("fLaC"))
	case "audio/aac", "audio/x-aac":
		return bytes.HasPrefix(header, []byte("ADIF")) || hasADTSFrameSync(header)
	case "audio/amr":
		return bytes.HasPrefix(header, []byte("#!AMR"))
	case "audio/aiff", "audio/x-aiff":
		return len(header) >= 12 && bytes.Equal(header[:4], []byte("FORM")) && (bytes.Equal(header[8:12], []byte("AIFF")) || bytes.Equal(header[8:12], []byte("AIFC")))
	case "audio/midi", "audio/mid":
		return bytes.HasPrefix(header, []byte("MThd"))
	default:
		return false
	}
}

func hasMPEGFrameSync(header []byte) bool {
	for i := 0; i+1 < len(header); i++ {
		if header[i] == 0xff && header[i+1]&0xe0 == 0xe0 {
			return true
		}
	}
	return false
}

func hasADTSFrameSync(header []byte) bool {
	for i := 0; i+1 < len(header); i++ {
		if header[i] == 0xff && header[i+1]&0xf6 == 0xf0 {
			return true
		}
	}
	return false
}
