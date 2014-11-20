package counter

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"appengine"
	"appengine/taskqueue"
	"appengine/urlfetch"
)

// Update these constants with values from your project!
const (
	// Bucket for storing generated thumbnails.
	saveToBucketName = "fifth-curve-684-output-bucket"
	// IP pointing to worker processing pool.
	processingPoolIp = "107.178.243.219"
)

func init() {
	http.HandleFunc("/", handler)
	http.HandleFunc("/worker", worker)
}

type Counter struct {
	BucketName string
	Count      int
}

type notification struct {
	Id             string `json:"id"`
	ObjectName     string `json:"name"`
	ObjectSelfLink string `json:"selfLink"`
	BucketName     string `json:"bucket"`
}

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
	c.Infof("%s: %s", n.Id, n.BucketName, n.ObjectName)

	//Set the Object Name to the selfLink encoded version of the Object Name. Be careful, because ObjectSelfLink could be encoded.
	n.ObjectName, err = url.QueryUnescape(filepath.Base(n.ObjectSelfLink))
	if err != nil {
		c.Errorf("Error attempting to build object name from self link %v: %v", n.ObjectSelfLink, err)
		return
	}

	//Create task assigned to "worker" task handler function
	v := url.Values{}
	v.Set("bucketName", n.BucketName)
	v.Set("objectName", n.ObjectName)
	v.Set("id", n.Id)
	t := taskqueue.NewPOSTTask("/worker", v)
	if _, err := taskqueue.Add(c, t, ""); err != nil {
		c.Errorf("%v", err)
		return
	}
}

func worker(w http.ResponseWriter, r *http.Request) {
	//Get App Engine context
	c := appengine.NewContext(r)
	//Get bucket name from request
	bucketName := r.FormValue("bucketName")
	objectName := r.FormValue("objectName")
	pathParts := []string{bucketName, objectName}
	id := strings.Join(pathParts, "/")

	//Create an image processing request
	client := urlfetch.Client(c)
	values := make(url.Values)
	values.Set("id", id)
	values.Set("save-to", saveToBucketName)

	//Create Post URL by combining HTTP protocol and processing pool IP address
	var postUrlParts []string
	postUrlParts = append(postUrlParts, "http://")
	postUrlParts = append(postUrlParts, processingPoolIp)
	postUrlParts = append(postUrlParts, "/process")
	postUrl := strings.Join(postUrlParts, "")

	c.Infof("Sending request to transform: %v", objectName)

	//Send the image processing request to the image processing web service
	resp, err := client.PostForm(postUrl, values)
	if err != nil {
		c.Errorf("Error sending POST to URL: %v", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	c.Infof("HTTP POST returned status: %v", resp.Status)
	if respBody, err := ioutil.ReadAll(resp.Body); err == nil {
		c.Infof("respBody=%v", string(respBody))
	} else {
		c.Errorf("Error attempting to read resp body: %v", err)
	}
	// Forward the backend service's status code along to the taskqueue.
	w.WriteHeader(resp.StatusCode)

	//TODO: Add Confirmation Queue to handle if assigned VM is deleted via Autoscaler scale down

}
