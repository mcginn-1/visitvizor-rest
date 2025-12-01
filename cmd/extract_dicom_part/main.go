package main

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
)

func main() {
	inPath := "testdata/dicomweb_frame1.bin"
	outPath := "testdata/dicomweb_frame1.dcm"

	f, err := os.Open(inPath)
	if err != nil {
		panic(fmt.Errorf("open %s: %w", inPath, err))
	}
	defer f.Close()

	// For multipart parsing we need the full Content-Type header value.
	// Use the value you saw in your test log:
	//   multipart/related; boundary=...; type="application/dicom"
	const contentType = `multipart/related; boundary=22d8f151d64a257ec4aaf5526657604e99ae9e7b4c49cb0ed0d53fddf37f; type="application/dicom"`

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		panic(fmt.Errorf("ParseMediaType: %w", err))
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		panic(fmt.Errorf("not multipart, got %q", mediaType))
	}

	boundary := params["boundary"]
	if boundary == "" {
		panic("no boundary in Content-Type")
	}

	// IMPORTANT: the raw file should not include HTTP headers, just the body.
	// Our test wrote exactly resp.Body, so this is fine.
	reader := multipart.NewReader(bufio.NewReader(f), boundary)

	part, err := reader.NextPart()
	if err == io.EOF {
		panic("no parts found in multipart")
	}
	if err != nil {
		panic(fmt.Errorf("NextPart: %w", err))
	}
	defer part.Close()

	// Optional: log part headers to verify it's application/dicom
	hdr := textproto.MIMEHeader(part.Header)
	fmt.Println("First part headers:")
	for k, v := range hdr {
		fmt.Printf("  %s: %s\n", k, strings.Join(v, ", "))
	}

	out, err := os.Create(outPath)
	if err != nil {
		panic(fmt.Errorf("create %s: %w", outPath, err))
	}
	defer out.Close()

	if _, err := io.Copy(out, part); err != nil {
		panic(fmt.Errorf("write %s: %w", outPath, err))
	}

	fmt.Printf("Wrote first DICOM part to %s\n", outPath)
}
