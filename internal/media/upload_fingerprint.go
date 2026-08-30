package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// imageUploadFingerprint hashes a staged image without consuming the body.
// prepareImageUpload returns an *os.File specifically so a retry can validate
// the same bytes and compare them with the request metadata saved in the job.
func imageUploadFingerprint(input UploadInput) (string, error) {
	file, ok := input.Body.(*os.File)
	if !ok {
		return uploadMetadataFingerprint(input), nil
	}
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, copyErr := io.CopyN(hasher, file, input.Size)
	_, seekErr := file.Seek(position, io.SeekStart)
	if copyErr != nil {
		return "", copyErr
	}
	if seekErr != nil {
		return "", seekErr
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func uploadMetadataFingerprint(input UploadInput) string {
	return fmt.Sprintf("meta:%d:%s:%s", input.Size, input.MimeType, filepath.Base(input.Filename))
}
