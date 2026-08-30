package media

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPrepareAudioUploadRequiresMatchingContainerHeader(t *testing.T) {
	tests := []struct {
		name string
		mime string
		body []byte
		ok   bool
	}{
		{name: "mp4", mime: "audio/mp4", body: []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}, ok: true},
		{name: "ogg", mime: "audio/ogg", body: []byte("OggS\x00\x00\x00\x00"), ok: true},
		{name: "webm", mime: "audio/webm", body: []byte{0x1a, 0x45, 0xdf, 0xa3, 0x93}, ok: true},
		{name: "wrong header", mime: "audio/ogg", body: []byte("plain text"), ok: false},
		{name: "unsupported MIME", mime: "audio/x-custom", body: []byte("audio"), ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := UploadInput{Filename: "sample.bin", MimeType: test.mime, Size: int64(len(test.body)), Body: bytes.NewReader(test.body)}
			prepared, cleanup, err := prepareAudioUpload(context.Background(), input)
			defer cleanup()
			if test.ok {
				if err != nil {
					t.Fatalf("valid audio rejected: %v", err)
				}
				if prepared.Body == nil || prepared.Size != input.Size {
					t.Fatalf("prepared input=%+v", prepared)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid audio err=%v", err)
			}
		})
	}
}

func TestCopyUploadBodyReadsOnlyDeclaredSizePlusOne(t *testing.T) {
	var dst bytes.Buffer
	source := bytes.NewReader(bytes.Repeat([]byte{'x'}, 32))
	written, err := copyUploadBody(context.Background(), &dst, source, 4)
	if err != nil {
		t.Fatal(err)
	}
	if written != 5 || dst.Len() != 5 {
		t.Fatalf("written=%d destination=%d, want 5", written, dst.Len())
	}
}
