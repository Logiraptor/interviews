package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/dustin/go-humanize"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/storage"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/iterator"
)

var (
	// iamService is a client for calling the signBlob API.
	iamService *iam.Service

	// serviceAccountName represents Service Account Name.
	// See more details: https://cloud.google.com/iam/docs/service-accounts
	serviceAccountName string

	// uploadableBucket is the destination bucket.
	// All users will upload files directly to this bucket by using generated Signed URL.
	uploadableBucket string

	// serviceAccountID follows the below format.
	// "projects/%s/serviceAccounts/%s"
	serviceAccountID string
)

func tmpl() *template.Template {
	return template.Must(template.New("root").ParseFiles("index.html"))
}

func main() {
	cred, err := google.DefaultClient(context.Background(), iam.CloudPlatformScope)
	if err != nil {
		log.Fatal(err)
	}
	iamService, err = iam.New(cred)
	if err != nil {
		log.Fatal(err)
	}

	uploadableBucket = os.Getenv("UPLOADABLE_BUCKET")
	serviceAccountName = os.Getenv("SERVICE_ACCOUNT")
	serviceAccountID = fmt.Sprintf(
		"projects/%s/serviceAccounts/%s",
		os.Getenv("GOOGLE_CLOUD_PROJECT"),
		serviceAccountName,
	)

	http.Handle("/static/", http.StripPrefix("/static", http.FileServer(http.Dir("static"))))
	http.HandleFunc("/signed-url", signHandler)
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/", indexHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func getSignedUrl(ctx context.Context, contentType string, name string) (string, error) {
	key := uuid.New().String()
	key += fmt.Sprintf("/%s", name)
	return signUrl(ctx, "PUT", contentType, key)
}

func signUrl(ctx context.Context, method, contentType, name string) (string, error) {
	url, err := storage.SignedURL(uploadableBucket, name, &storage.SignedURLOptions{
		GoogleAccessID: serviceAccountName,
		Method:         method,
		Expires:        time.Now().Add(time.Hour),
		ContentType:    contentType,
		// To avoid management for private key, use SignBytes instead of PrivateKey.
		// In this example, we are using the `iam.serviceAccounts.signBlob` API for signing bytes.
		// If you hope to avoid API call for signing bytes every time,
		// you can use self hosted private key and pass it in Privatekey.
		SignBytes: func(b []byte) ([]byte, error) {
			resp, err := iamService.Projects.ServiceAccounts.SignBlob(
				serviceAccountID,
				&iam.SignBlobRequest{BytesToSign: base64.StdEncoding.EncodeToString(b)},
			).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return base64.StdEncoding.DecodeString(resp.Signature)
		},
	})
	return url, err
}

func signHandler(w http.ResponseWriter, r *http.Request) {
	ct := r.FormValue("content_type")
	if ct == "" {
		http.Error(w, "content_type must be set", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	url, err := getSignedUrl(r.Context(), ct, name)
	if err != nil {
		log.Printf("sign: failed to sign, err = %v\n", err)
		http.Error(w, "failed to sign by internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, url)
}

func indexHandler(rw http.ResponseWriter, req *http.Request) {
	jobs, err := getStatus(req.Context())

	var buf = new(bytes.Buffer)
	err = tmpl().ExecuteTemplate(buf, "index.html", map[string]interface{}{
		"objects": jobs,
	})
	if err != nil {
		fmt.Println(err.Error())
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	io.Copy(rw, buf)
}

func getStatus(ctx context.Context) ([]job, error) {

	sclient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	var objects []object

	it := sclient.Bucket(uploadableBucket).Objects(ctx, &storage.Query{})
	for {
		attrs, err := it.Next()
		if err != nil {
			if err == iterator.Done {
				break
			}

			return nil, err
		}
		id := path.Dir(attrs.Name)
		name := path.Base(attrs.Name)
		objects = append(objects, object{attrs, id, name})
	}
	jobs := resolveProgress(ctx, objects)
	return jobs, nil
}

func statusHandler(rw http.ResponseWriter, req *http.Request) {
	jobs, err := getStatus(req.Context())
	if err != nil {
		httpError(rw, err)
		return
	}
	var buf = new(bytes.Buffer)
	err = tmpl().ExecuteTemplate(buf, "table", jobs)
	if err != nil {
		fmt.Println(err.Error())
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	io.Copy(rw, buf)
}

type object struct {
	obj  *storage.ObjectAttrs
	ID   string
	Name string
}

type job struct {
	ctx        context.Context
	Uploaded   time.Time
	ID         string
	Name       string
	Original   *storage.ObjectAttrs
	Flac       *storage.ObjectAttrs
	Progress   *storage.ObjectAttrs
	Transcript *storage.ObjectAttrs
}

func (j job) ResultUrl() string {
	if j.Transcript == nil {
		return ""
	}
	url, err := signUrl(j.ctx, "GET", "", j.Transcript.Name)
	if err != nil {
		return "???"
	}
	return url
}

func (j job) Status() string {
	if j.Transcript != nil {
		return "done"
	}
	if j.Progress != nil {
		return "transcribing"
	}
	if j.Flac != nil {
		return "converting"
	}
	return "queued"
}

func (j job) RelativeTime() string {
	return humanize.Time(j.Uploaded)
}

func (j job) Percent() string {
	if j.Progress == nil {
		return ""
	}
	perc, err := getProgress(j.ctx, j.Progress)
	if err != nil {
		fmt.Println(err)
		return "?"
	}
	return fmt.Sprintf("%d%%", perc)
}

func resolveProgress(ctx context.Context, objects []object) []job {
	// ORIGINAL AUDIO:
	// id: GUID
	// name: something.other

	// FLAC AUDIO:
	// id: GUID
	// name: something_output.flac

	// PROGRESS:
	// id: GUID
	// name: something_output.flac.progress

	// TRANSCRIPT:
	// id: GUID
	// name: something_output.txt
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].obj.Created.Before(objects[j].obj.Created)
	})

	var objectsById = make(map[string][]object)
	for _, obj := range objects {
		objectsById[obj.ID] = append(objectsById[obj.ID], obj)
	}

	var jobs []job
outer:
	for id, set := range objectsById {

		var job = job{
			ctx:      ctx,
			ID:       id,
			Uploaded: set[0].obj.Created,
			Name:     set[0].Name,
		}

		job.Original = set[0].obj

		for _, obj := range set {
			if strings.HasSuffix(obj.Name, ".txt") {
				job.Transcript = obj.obj
				jobs = append(jobs, job)
				continue outer
			}
		}

		for _, obj := range set {
			if strings.HasSuffix(obj.Name, ".progress") {
				job.Progress = obj.obj
				jobs = append(jobs, job)
				continue outer
			}
		}
		for _, obj := range set {
			if strings.HasSuffix(obj.Name, ".flac") {
				job.Flac = obj.obj
				jobs = append(jobs, job)
				continue outer
			}
		}

		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].Uploaded.Before(jobs[j].Uploaded)
	})

	return jobs
}

func getProgress(ctx context.Context, obj *storage.ObjectAttrs) (int32, error) {
	client, err := speech.NewClient(ctx)
	if err != nil {
		return 0, err
	}

	sclient, err := storage.NewClient(ctx)
	if err != nil {
		return 0, err
	}
	bucket := sclient.Bucket(uploadableBucket)

	progressObject := bucket.Object(obj.Name)
	rd, err := progressObject.NewReader(ctx)
	if err != nil {
		return 0, err
	}
	defer rd.Close()
	operationName, err := ioutil.ReadAll(rd)
	if err != nil {
		return 0, err
	}

	op := client.LongRunningRecognizeOperation(string(operationName))
	_, err = op.Poll(ctx)
	if err != nil {
		return 0, err
	}
	metadata, err := op.Metadata()
	if err != nil {
		return 0, err
	}

	return metadata.ProgressPercent, nil
}

func httpError(rw http.ResponseWriter, err error) {
	log.Println(err)
	http.Error(rw, err.Error(), http.StatusInternalServerError)
}
