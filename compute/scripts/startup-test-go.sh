#!/bin/bash

apt-get -y update
apt-get -y install imagemagick
apt-get -y install mercurial

curl -O https://storage.googleapis.com/golang/go1.2.2.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.2.2.linux-amd64.tar.gz

export PATH=$PATH:/usr/local/go/bin
export GOPATH=/usr/local
GMV=/usr/share/google/get_metadata_value

#Get Developers Console Project Service Account
go get code.google.com/p/goauth2/compute/serviceaccount

#Get go API client for Google Cloud Storage
go get code.google.com/p/google-api-go-client/storage/v1

# Restart the server in the background if it fails.
cd /tmp
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
