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
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/storage/v1"
)

const (
	numCopiers = 10
	numFiles   = 10000
	usage      = `
Usage:
	go run generate_files.go BUCKET PATH/TO/IMAGE
Where BUCKET is the GCS bucket in which to generate files and PATH/TO/IMAGE is 
the path to the image file we wish to duplicate.
`
)

type GCSCopyReq struct {
	SourceBucket, SourceFile, DestBucket, DestFile string
}

func buildName(prefix int, name string) string {
	return strings.Join([]string{strconv.Itoa(prefix), name}, "-")
}

// copyObjects takes copy requests from the input channel and attempts to use
// the GCS Storage API to perform the action. It incorporates naive retry logic
// and will output failures to the outut channel.
func copyObjects(s *storage.Service, in <-chan *GCSCopyReq, out chan<- string, fin chan<- interface{}) {
	var err error
	for o := range in {
		for i := 0; i < 3; i++ {
			if _, err = s.Objects.Copy(o.SourceBucket, o.SourceFile, o.DestBucket, o.DestFile, nil).Do(); err == nil {
				break
			}
		}
		if err != nil {
			out <- o.DestFile
		} else {
			fin <- struct{}{}
		}
	}
}

func main() {
	flag.Parse()
	if flag.NArg() != 2 {
		log.Fatalf("Please specify both required arguments." + usage)
	}
	bucket := flag.Arg(0)
	imagePath := flag.Arg(1)
	file, err := os.Open(imagePath)
	if err != nil {
		log.Fatalf("Error opening image file: %v", err)
	}
	fileName := path.Base(imagePath)
	defer file.Close()
	service, err := storage.New(oauth2.NewClient(oauth2.NoContext, google.ComputeTokenSource("")))
	if err != nil {
		log.Fatalf("Failed to create GCS client: %v", err)
	}
	// Insert the image into GCS.
	baseFileName := buildName(0, fileName)
	_, err = service.Objects.Insert(bucket, &storage.Object{Name: baseFileName}).Media(file).Do()
	if err != nil {
		log.Fatalf("Unable to upload initial file to bucket: %v", err)
	}
	c := make(chan *GCSCopyReq, 999)
	f := make(chan string)
	finished := make(chan interface{})
	wg := &sync.WaitGroup{}
	wg.Add(numCopiers)
	for i := 0; i < numCopiers; i++ {
		go func() {
			copyObjects(service, c, f, finished)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(f)
		close(finished)
	}()
	go func() {
		i := 0
		for _ = range finished {
			i++
			if i%100 == 0 {
				fmt.Printf("%v/%v copied.\n", i, numFiles)
			}
		}
		fmt.Printf("%v/%v copied.\n", i, numFiles)
	}()
	for i := 1; i < numFiles; i++ {
		c <- &GCSCopyReq{
			SourceBucket: bucket,
			SourceFile:   baseFileName,
			DestBucket:   bucket,
			DestFile:     buildName(i, fileName),
		}
	}
	close(c)
	for errFile := range f {
		fmt.Printf("Could not copy to %v\n", errFile)
	}
}
