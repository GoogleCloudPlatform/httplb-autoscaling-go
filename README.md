## Google Cloud HTTP Load Balancer and Autoscaling Example

### Motivation

In this sample, we will explore an application which performs several transformations on a set of input images.

Images are stored in Google Cloud Storage buckets and are processed by a scaled and load balanced set of Compute Engine resources. Watching for new images and requesting their transformation is orchestrated by a simple App Engine application.

### Setup

#### Install Tools, Create Project

1. Install the gcloud command line tool: <https://developers.google.com/cloud/sdk/#Quick\_Start>
2. Install the gcloud preview commands:

		gcloud components update preview
		gcloud components update app
3. Create a project on Cloud Console <https://console.developers.google.com/project>
4. Enable billing.
5. Enable Google Compute Engine Instance Groups API
6. Enable Google Compute Engine Instance Group Manager API
7. Enable Google Compute Engine Autoscaler API
8. Create an Oauth Service Account for your project.
9. Set the project's ID:

		export PROJECT_ID=[your-project-id]
		export ZONE="us-central1-f"
		gcloud config set project ${PROJECT_ID}
		gcloud config set compute/zone ${ZONE}

#### Create Input/Output Buckets

	export TMP_BUCKET="${PROJECT_ID}-tmp-bucket"
	export INPUT_BUCKET="${PROJECT_ID}-input-bucket"
	export OUTPUT_BUCKET="${PROJECT_ID}-output-bucket"
	gsutil mb gs://${TMP_BUCKET} gs://${INPUT_BUCKET} gs://${OUTPUT_BUCKET}
	gcloud compute project-info add-metadata --metadata tmp_bucket=${TMP_BUCKET}

#### Google Compute Engine Pool

##### Create the Managed Instance Group
1. Create the instance template for our backends:

		gcloud compute instance-templates create imagemagick-go-template \
		--description "A pool of machines running our ImageMagick service." \
		--image debian-7 \
		--machine-type n1-standard-1 \
		--metadata goprog="http://storage.googleapis.com/imagemagick/compute/web-process-image.go" \
		startup-script-url="gs://imagemagick/compute/scripts/startup-test-go.sh" \
		--boot-disk-size 200GB \
		--scopes storage-full \
		--tags http-lb
2. Create the Managed Instance Group:

		gcloud preview managed-instance-groups create imagemagick-go \
		--base-instance-name imagemagick-go \
		--size 1 \
		--template imagemagick-go-template

##### Create the HTTP Load Balancer
1. Spin up a backend service:
  1. Create a healh check:

		  gcloud compute http-health-checks create imagemagick-check \
		  --request-path "/healthcheck"
  2. Create the backend service:

		  gcloud compute backend-services create imagemagick-backend-service \
		  --http-health-check imagemagick-check
  3. Add the managed instance group to the backend service:

		  gcloud compute backend-services add-backend imagemagick-backend-service \
		  --group imagemagick-go \
		  --balancing-mode UTILIZATION \
		  --max-utilization 0.6
2. Create a URL map to route requests to the appropriate backend services:

		gcloud compute url-maps create imagemagick-map \
		--default-service imagemagick-backend-service
3. Create a target HTTP proxy:

		gcloud compute target-http-proxies create imagemagick-proxy \
		--url-map imagemagick-map
4. Create a global forwarding rule:

		gcloud compute forwarding-rules create imagemagick-rule \
		--global \
		--target-http-proxy imagemagick-proxy \
		--port-range 80
5. Create a firewall to allow access to port 80 of all instances tagged with `http-lb`:

		gcloud compute firewall-rules create http-lb-rule \
		--target-tags http-lb \
		--allow tcp:80

Note: It can take several minutes for the instances to be marked as healthy in the backend service.

##### Set up the Autoscaler

	gcloud preview autoscaler create imagemagick-go-autoscaler \
	--max-num-replicas 23 \
	--min-num-replicas 5 \
	--target-load-balancer-utilization 0.5 \
	--target "https://www.googleapis.com/replicapool/v1beta2/projects/${PROJECT_ID}/zones/us-central1-f/instanceGroupManagers/imagemagick-go"

#### AppEngine

##### Update main.go consts

1. Update processingPoolIp in main.go with the IP address created for our global forwarding rule. You can look this up by running:

		gcloud compute forwarding-rules list
2. Update saveToBucketName with `${OUTPUT_BUCKET}`

##### Deploy

	gcloud preview app deploy appengine/

##### Create Object Change Notification

An Object Change Notification will allow our AppEngine app to be notified when files are added or removed from the input bucket. Setting up this notification is a several step process which involves creating a service account, verifying our domain with Google Webmaster Tools, and finally using gsutil to watch the bucket.

###### Create a Service Account

<https://cloud.google.com/storage/docs/object-change-notification#\_Service\_Account>

###### Verify the domain

<https://cloud.google.com/storage/docs/object-change-notification#\_Authorize\_Endpoint>

Be sure to verify the HTTPS version of your domain.

###### Create the notification

1. Configure gsutil to use the Service Account:

<https://cloud.google.com/storage/docs/object-change-notification#\_Using\_Account>

2. Watch the bucket:

		gsutil notification watchbucket \
		https://${PROJECT_ID}.appspot.com/ gs://${INPUT_BUCKET}

### Running

First we need to generate a set of images to process. Since it's simplest, we'll create a bunch of duplicates in a temporary bucket. When we want to run the demo, we'll use gsutil to copy those images in parallel from the temp bucket to the input one.

To create a bunch of temporary images, ssh into any GCE instance currently running in your project (Hint: you can even use the Developers Console to do this in the browser). Next, run the following commands. Note that since `${TMP_BUCKET}` is only defined for your local shell, you'll have to copy/paste it over.

	export GOPATH=/usr/local
	export PATH=$PATH:/usr/local/go/bin
	go run /tmp/generate_files.go `/usr/share/google/get_metadata_value attributes/tmp_bucket` /tmp/eiffel.jpg

Go ahead and close your ssh connection to the GCE instance. The remaining commands must be run from your local shell.

Generate some load! The following command will copy image files from a public GCS bucket to the project's input bucket, where they will be processed. 

	gsutil -m cp -R gs://${TMP_BUCKET}/* gs://${INTPUT_BUCKET}

### Observe

Keep a close eye on your project's VM instances screen. You should see many more get spun up within a minute or so--Autoscaler's default cool down period between resizing attempts.

### Cleanup

Simply delete the project using the [Google Developers Console](https://console.developers.google.com).

<https://developers.google.com/console/help/new/#creatingdeletingprojects>


### Troubleshooting


### Contributing changes

* See [CONTRIB.md](CONTRIB.md)


### Licensing

* See [LICENSE](LICENSE)
