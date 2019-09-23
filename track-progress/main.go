package trackprogress

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/storage"
)

func TrackProgress(ctx context.Context, input struct{}) error {

	log.Println("Tracking!")

	// Creates a client.
	client, err := speech.NewClient(ctx)
	if err != nil {
		return err
	}

	sclient, err := storage.NewClient(ctx)
	if err != nil {
		return err
	}

	bucket := sclient.Bucket(os.Getenv("PROGRESS_BUCKET"))
	iter := bucket.Objects(ctx, &storage.Query{Prefix: "progress-"})
	for {
		attrs, err := iter.Next()
		if err != nil {
			return err
		}

		log.Println(attrs.Name)
		progressObject := bucket.Object(attrs.Name)
		rd, err := progressObject.NewReader(ctx)
		if err != nil {
			return err
		}
		defer rd.Close()
		operationName, err := ioutil.ReadAll(rd)
		if err != nil {
			return err
		}
		log.Println("Found operation: ", operationName)

		op := client.LongRunningRecognizeOperation(string(operationName))
		resp, err := op.Poll(ctx)
		if err != nil {
			return err
		}

		if resp != nil {
			log.Println("Done")
			transcriptName := strings.TrimPrefix(attrs.Name, "progress-") + ".txt"
			wr := bucket.Object(transcriptName).NewWriter(ctx)
			if err != nil {
				return err
			}
			defer wr.Close()

			results := resp.GetResults()
			for _, res := range results {
				alts := res.GetAlternatives()
				for _, alt := range alts {
					_, err := fmt.Fprintln(wr, alt.GetTranscript())
					if err != nil {
						return err
					}
				}
			}
			progressObject.Delete(ctx)
			log.Println("Wrote", transcriptName)
		} else {
			log.Println("Not Done yet")
		}
	}

	return nil
}
