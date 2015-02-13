#!/bin/bash

# Copyright 2014 Google Inc. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

apt-get -y update
apt-get -y install imagemagick
apt-get -y install mercurial
apt-get -y install git

curl -O https://storage.googleapis.com/golang/go1.2.2.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.2.2.linux-amd64.tar.gz

export PATH=$PATH:/usr/local/go/bin
export GOPATH=/usr/local
GMV=/usr/share/google/get_metadata_value

#Get Developers Console Project Service Account
go get code.google.com/p/goauth2/compute/serviceaccount

#Get go API client for Google Cloud Storage
go get code.google.com/p/google-api-go-client/storage/v1

# Get the go code to generate our initial image load.
#go get github.com/GoogleCloudPlatform/httplb-autoscaling-go/scripts
go get golang.org/x/oauth2
go get golang.org/x/oauth2/google
go get google.golang.org/api/storage/v1

# Download our files to a temp dir.
cd /tmp

curl -O http://storage.googleapis.com/imagemagick/compute/scripts/eiffel.jpg
curl -O http://storage.googleapis.com/imagemagick/compute/scripts/generate_files.go

# Restart the server in the background if it fails.
function runServer {
  while :
  do
    #Get Go program URL from instance Metadata
    GOURL=$($GMV attributes/goprog)

    #Download Go program
    curl -O $GOURL
    #Get Filename
    GOPROG=${GOURL##*/}

    #Build the go command and run it
    CMDLINE="go run ./$GOPROG"
    echo "Running $CMDLINE"
    $CMDLINE
  done
}
runServer &
