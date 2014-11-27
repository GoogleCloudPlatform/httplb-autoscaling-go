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

// Package main is responsible for orchestrating our App Engine app. It
// provides functions for handling GCS and task queue notifications.
package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"appengine"
	"appengine/delay"
	"appengine/urlfetch"
)

// Update these constants with values from your project!
const (
	// Bucket for storing generated thumbnails.
	saveToBucketName = "fifth-curve-684-output-bucket"
	// IP pointing to worker processing pool.
	processingPoolIp = "107.178.243.219"
)

var (
	transformImageFunc = delay.Func("transform", transformImage)
)

func init() {
	http.HandleFunc("/", handler)
}

type notification struct {
	ID             string `json:"id"`
	ObjectName     string `json:"name"`
	ObjectSelfLink string `json:"selfLink"`
	BucketName     string `json:"bucket"`
}

// handler processes a Cloud Storage Object Change Notification and pushes a
// corresponding transformation task to the default task queue.
func handler(w http.ResponseWriter, r *http.Request) {
	//Get App Engine context
	c := appengine.NewContext(r)

	//Handle Cloud Storage Object Change Notifications
	//Get the HTTP Post resource state
	resourceState := r.Header.Get("X-Goog-Resource-State")
	c.Infof("Cloud Storage Notification: %v", resourceState)
	// Silently discard notifications we don't care about.
	if resourceState != "exists" {
		return
	}
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		c.Errorf("Error attempting to read req body: %v", err)
		return
	}
	c.Infof("request: %v", string(b))

	//Initialize a notification struct
	n := &notification{}
	if err := json.Unmarshal(b, n); err != nil {
		c.Errorf("Error unmarshalling JSON: %v", err)
		return
	}
	c.Infof("%s: %s", n.ID, n.BucketName, n.ObjectName)

	//Set the Object Name to the selfLink encoded version of the Object Name. Be careful,
	// because ObjectSelfLink could be encoded.
	n.ObjectName, err = url.QueryUnescape(filepath.Base(n.ObjectSelfLink))
	if err != nil {
		c.Errorf("Error attempting to build object name from self link %v: %v",
			n.ObjectSelfLink, err)
		return
	}

	transformImageFunc.Call(c, *n)
}

// transformImage takes a notification to manipulate an image and asks our backend service to
// compute some transformation on it via HTTP. If the service is unavailable, it returns an error.
func transformImage(c appengine.Context, n notification) (err error) {
	id := strings.Join([]string{n.BucketName, n.ObjectName}, "/")

	//Create an image processing request
	client := urlfetch.Client(c)
	values := url.Values{
		"id":      {id},
		"save-to": {saveToBucketName},
	}

	//Create Post URL by combining HTTP protocol and processing pool IP address
	postUrlParts := []string{"http://", processingPoolIp, "/process"}
	postUrl := strings.Join(postUrlParts, "")

	c.Infof("Sending request to transform: %v", n.ObjectName)

	//Send the image processing request to the image processing web service
	resp, err := client.PostForm(postUrl, values)
	if err != nil {
		c.Errorf("Error sending POST to URL: %v", err)
		return
	}
	c.Infof("HTTP POST returned status: %v", resp.Status)
	if resp.StatusCode != http.StatusOK {
		err = errors.New("Non-200 response from backend")
		return
	}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		c.Errorf("Error attempting to read resp body: %v", err)
		return
	}
	c.Infof("respBody=%v", string(respBody))
	//TODO: Add Confirmation Queue to handle if assigned VM is deleted via Autoscaler scale down
	return
}
