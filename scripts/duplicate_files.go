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

// Binary main uses the provided service account key to duplicate all of
// the files in the indicated bucket. It uses several concurrent copiers and
// provides for a naive retry mechanism.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"
	"sync"

	"code.google.com/p/google-api-go-client/storage/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	numCopiers = 5
)

var (
	keyFile = flag.String("key-file", "", "The path to the user's service account JSON key.")
	bucket  = flag.String("bucket", "", "The bucket to duplicate files in.")
)

// getObjects returns a slice of all objects in the given bucket.
func getObjects(s *storage.Service, b string) (objs []*storage.Object) {
	resp, err := s.Objects.List(b).Do()
	if err != nil {
		log.Fatalf("Unable to get object listing for bucket %s: %v\n", b, err)
	}
	objs = make([]*storage.Object, 0, 1000)
	objs = append(objs, resp.Items...)
	for resp.NextPageToken != "" {
		resp, err = s.Objects.List(b).PageToken(resp.NextPageToken).Do()
		if err != nil {
			log.Printf("Unable to get subsequent listing for bucket: %v\n", err)
			break
		}
		objs = append(objs, resp.Items...)
	}
	fmt.Printf("Got %v objects\n", len(objs))
	return
}

// copyObjects copies the source file to the destination in Google Cloud Storage.
// It returns an error if one occurred.
func copyObject(s *storage.Service, sourceBucket, sourceName, destBucket, destName string) (err error) {
	// Try 3 times to copy.
	for i := 0; i <= 3; i++ {
		if _, err = s.Objects.Copy(sourceBucket, sourceName, destBucket, destName, nil).Do(); err == nil {
			break
		}
	}
	return
}

// duplicateFiles makes copies of files from the given channel. The resultant
// copies are prefixed to avoid collisions. Failures are written to out.
func duplicateFiles(s *storage.Service, prefix string, in <-chan *storage.Object, out chan<- string) {
	for o := range in {
		if err := copyObject(s, o.Bucket, o.Name, o.Bucket, strings.Join([]string{prefix, o.Name}, "-")); err != nil {
			out <- o.Name
		}
	}
	fmt.Printf("copier %v exiting.\n", prefix)
}

func main() {
	flag.Parse()
	bytes, err := ioutil.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("Error reading keyFile: %v\n", err)
	}
	conf, err := google.JWTConfigFromJSON(bytes, storage.DevstorageFull_controlScope)
	if err != nil {
		log.Fatalf("Could not build JWT config: %v\n", err)
	}
	service, err := storage.New(conf.Client(oauth2.NoContext))
	if err != nil {
		log.Fatalf("Failed to create GCS client: %v\n", err)
	}

	objects := getObjects(service, *bucket)
	c := make(chan *storage.Object)
	f := make(chan string)
	wg := &sync.WaitGroup{}
	wg.Add(numCopiers)
	for i := 0; i < numCopiers; i++ {
		go func(name string) {
			duplicateFiles(service, name, c, f)
			wg.Done()
		}(strconv.Itoa(i))
	}
	go func() {
		wg.Wait()
		close(f)
	}()
	go func() {
		defer close(c)
		for _, o := range objects {
			c <- o
		}
	}()
	for errFile := range f {
		fmt.Printf("Could not copy %v\n", errFile)
	}
}
