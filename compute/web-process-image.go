// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"code.google.com/p/goauth2/compute/serviceaccount"
	storage "code.google.com/p/google-api-go-client/storage/v1"
)

const (
	NumImageProcessors    = 2
	ImageProcessQueueSize = 50
	ThumbnailSuffix       = "-t"
)

var (
	hostname string
	// A map of HTTP response codes which we consider to be retryable.
	retryableCodes = map[int]bool{
		http.StatusForbidden:           true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	}
)

// RetryTransport wraps http.DefaultTransport and provides for retrying HTTP requests up to maxTries times.
type RetryTransport struct {
	http.RoundTripper
	maxTries int
}

// RoundTrip implements the http.RoundTripper interface and will attempt to retry an HTTP request
// if the response contains a retryable status code.
func (t *RetryTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	var body []byte
	if req.Body != nil {
		body, err = ioutil.ReadAll(req.Body)
		if err != nil {
			return
		}
	}
	for i := 0; i < t.maxTries; i++ {
		if i != 0 {
			// Build a new request.
			req = copyRequest(req, body)
		}
		resp, err = t.RoundTripper.RoundTrip(req)
		if err == nil {
			if !retryableCodes[resp.StatusCode] {
				break // Success!
			}
			resp.Body.Close()
		}
	}
	return
}

// copyRequest constructs a new HTTP request mirroring the provided one with the given body.
func copyRequest(req *http.Request, body []byte) *http.Request {
	nreq, err := http.NewRequest(req.Method, req.URL.String(), bytes.NewReader(body))
	if err != nil {
		log.Panicf("Unable to copy http request: %v", err)
	}
	for k, vv := range req.Header {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		nreq.Header[k] = vv2
	}
	return nreq
}

// A processImageReq represents a request to perform some transformation on a given image.
type processImageReq struct {
	sourceBucket, filename, saveToBucket, saveToFilename string
}

// getProcessImageReq attempts to parse a processImageReq from the provided request.
func getProcessImageReq(r *http.Request) (processImageReq, error) {
	// Pull vars from the request.
	objectPath := r.FormValue("id")
	if objectPath == "" {
		return processImageReq{}, errors.New("Request did not provide image id.")
	}
	sourceBucket, filename := path.Split(objectPath)
	sourceBucket = strings.Trim(sourceBucket, "/")
	saveToBucket := r.FormValue("save-to")

	// Now get the filename extension.
	extension := filepath.Ext(filename)
	name := filename[:len(filename)-len(extension)]
	filenameElements := []string{name, ThumbnailSuffix, extension}
	saveToFilename := strings.Join(filenameElements, "")
	return processImageReq{
		sourceBucket:   sourceBucket,
		filename:       filename,
		saveToBucket:   saveToBucket,
		saveToFilename: saveToFilename,
	}, nil
}

// imagemagickHandler implements the http.Handler interface and provides for farming image
// manipulation requests out among a static number of goroutines.
type imagemagickHandler struct {
	c chan<- processImageReq
}

// ServeHTTP attempts to process an image manipulation request and returns a 200. If the request
// could not be queued, a 503 is returned; if the request was otherwise invalid, a 500.
func (h *imagemagickHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := getProcessImageReq(r)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	select {
	case h.c <- req:
		fmt.Fprintf(w, "hostname=%s", hostname)
	default:
		w.WriteHeader(503)
	}
}

// NewImagemagickHandler builds returns a new imagemagickHandler with the specified queueSize and
// number of processing routines.
func NewImagemagickHandler(queueSize, numRoutines int) (h *imagemagickHandler) {
	c := make(chan processImageReq, queueSize)
	h = &imagemagickHandler{c: c}
	for i := 0; i < numRoutines; i++ {
		p := NewImageProcessor(c, fmt.Sprintf("Processor(%d)", i))
		go p.process()
	}
	return
}

type imageProcessor struct {
	c      <-chan processImageReq
	client *http.Client
	s      *storage.Service
	l      *log.Logger
}

// process reads from the imageProcessor's input channel and attempts to process an image.
func (p *imageProcessor) process() {
	for r := range p.c {
		t := time.Now()
		if err := p.processImage(r); err != nil {
			p.l.Printf("Could not process image %v: %v\n", r.saveToFilename, err)
		}
		p.l.Printf("Processing took %fs\n", time.Since(t).Seconds())
	}
}

// getImageBytes attempts to download and return the bytes of the indicated GCS object. It may
// panic if a network request cannot be completed within 4 attempts.
func (p *imageProcessor) getImageBytes(sourceBucket, filename string) (b []byte) {
	obj, err := p.s.Objects.Get(sourceBucket, filename).Do()
	if err != nil {
		p.l.Panicf("Unable to get object %v from GCS: %v\n", filename, err)
	}
	resp, err := p.client.Get(obj.MediaLink)
	if err != nil {
		p.l.Panicf("Unable to download %v: %v\n", obj.MediaLink, err)
	}
	defer resp.Body.Close()
	b, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		p.l.Panicf("Unable to read body of %v: %v\n", obj.MediaLink, err)
	}
	return
}

// getThumbnailCommand returns a simple imagemagick command which resizes the indicated image into
// a 100x100 thumbnail.
func thumbnailCommand(in, out string) *exec.Cmd {
	return exec.Command("convert", in, "-thumbnail", "100x100", out)
}

// getIntenseCommand returns an imagemagick command which applies many CPU intensive
// transformations to the indicated image before resizing it into a 100x100 thumbnail. It should
// take about 7.8s to process on an n1-standard-1 machine.
func intenseCommand(in, out string) *exec.Cmd {
	return exec.Command("convert", in, "-auto-level", "-auto-orient", "-antialias",
		"-auto-gamma", "-contrast", "-despeckle", "-thumbnail", "100x100", out)
}

// getModerateCommand returns a an imagemagick command which applies several basic transformations
// to an image before resizing to a 100x100 thumbnail. It should take about 1s to process on an
// n1-standard-1 machine.
func moderateCommand(in, out string) *exec.Cmd {
	return exec.Command("convert", in, "-auto-orient", "-antialias", "-contrast", "-thumbnail",
		"100x100", out)
}

// processImage applies a transformation to the indicated image and writes its output to a given
// GCS bucket. It works in several steps:
// 1. Retrieve the image's data from GCS.
// 2. Downloads the image.
// 3. Uses Imagemagick to compute a transformation on the image.
// 4. Uploads the resulting image to GCS.
func (p *imageProcessor) processImage(r processImageReq) (err error) {
	// Copy the file to VM's attached Persistent Disk for image conversion
	b := p.getImageBytes(r.sourceBucket, r.filename)
	p.l.Printf("Read %d bytes from response body...\n", len(b))

	if err = ioutil.WriteFile(r.filename, b, 0600); err != nil {
		p.l.Printf("Error writing file %v to disk\n", r.filename)
		return
	}
	defer os.Remove(r.filename) // Cleanup input file after we transform it.

	cmd := moderateCommand(r.filename, r.saveToFilename)

	out, err := cmd.CombinedOutput()
	if err != nil {
		p.l.Printf("Could not transform file. StdOut: %v\n", string(out))
		return
	} else {
		p.l.Printf("Converted %s to %s\n", r.filename, r.saveToFilename)
	}
	defer os.Remove(r.saveToFilename) // Clean up after ourselves.

	// Upload the converted image file to Cloud Storage output bucket
	p.l.Println("Now starting upload to save-to Cloud Storage Bucket...")
	object := &storage.Object{Name: r.saveToFilename}
	file, err := os.Open(r.saveToFilename)
	if err != nil {
		p.l.Printf("Error opening %q\n", r.saveToFilename)
		return
	}
	defer file.Close()
	res, err := p.s.Objects.Insert(r.saveToBucket, object).Media(file).Do()
	if err != nil {
		p.l.Printf("Unable to upload %v to GCS\n", r.saveToFilename)
	}
	p.l.Printf("Created object %v at location %v\n", res.Name, res.SelfLink)
	return
}

// NewImageProcessor constructs an imageProcessor which listens for input on the provided channel
// and logs to stderr with its name as the prefix.
func NewImageProcessor(c <-chan processImageReq, name string) *imageProcessor {
	client, err := serviceaccount.NewClient(&serviceaccount.Options{
		Transport: &RetryTransport{http.DefaultTransport, 5},
	})
	if err != nil {
		log.Panicf("Failed to create service account client: %v\n", err)
	}
	service, err := storage.New(client)
	if err != nil {
		log.Panicf("Failed to create GCS client: %v\n", err)
	}
	return &imageProcessor{
		c:      c,
		client: client,
		s:      service,
		l:      log.New(os.Stderr, name, log.LstdFlags),
	}

}

// healthHandler writes an HTTP 200 response indicating general system healthiness.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func main() {
	var err error
	hostname, err = os.Hostname()
	if err != nil {
		log.Fatalf("Failed to get hostname: %v.\n", err)
	}
	h := NewImagemagickHandler(ImageProcessQueueSize, NumImageProcessors)
	http.Handle("/process", h)
	http.HandleFunc("/healthcheck", healthHandler)
	err = http.ListenAndServe(":80", nil)

	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
