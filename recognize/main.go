package recognize

import (
	"cloud.google.com/go/functions/metadata"
	"cloud.google.com/go/storage"
	speech "cloud.google.com/go/speech/apiv1"
	"context"
	"fmt"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1"
	"log"
	"os"
	"time"
)

// GCSEvent is the payload of a GCS event.
type GCSEvent struct {
	Bucket         string    `json:"bucket"`
	Name           string    `json:"name"`
	Metageneration string    `json:"metageneration"`
	ResourceState  string    `json:"resourceState"`
	TimeCreated    time.Time `json:"timeCreated"`
	Updated        time.Time `json:"updated"`
}

// RecognizeAudio prints information about a GCS event then submits a long running audio task
func RecognizeAudio(ctx context.Context, e GCSEvent) error {
	meta, err := metadata.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("metadata.FromContext: %v", err)
	}
	log.Printf("Event ID: %v\n", meta.EventID)
	log.Printf("Event type: %v\n", meta.EventType)
	log.Printf("Bucket: %v\n", e.Bucket)
	log.Printf("File: %v\n", e.Name)
	log.Printf("Metageneration: %v\n", e.Metageneration)
	log.Printf("Created: %v\n", e.TimeCreated)
	log.Printf("Updated: %v\n", e.Updated)
	return processAudio(ctx, e)
}

func processAudio(ctx context.Context, e GCSEvent) error {

	// Creates a client.
	client, err := speech.NewClient(ctx)
	if err != nil {
		log.Println("Failed to create client: %v", err)
		return err
	}

	uri := fmt.Sprintf("gs://%s/%s", e.Bucket, e.Name)

	// Detects speech in the audio file.
	op, err := client.LongRunningRecognize(ctx, &speechpb.LongRunningRecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:        speechpb.RecognitionConfig_FLAC,
			LanguageCode:    "en-US",
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Uri{Uri: uri},
		},
	})
	if err != nil {
		log.Println("failed to start long running recognize: %v", err)
		return err
	}

	sclient, err := storage.NewClient(ctx)
	if err != nil {
		return err
	}

	wr := sclient.Bucket(os.Getenv("PROGRESS_BUCKET")).Object("progress-" + e.Name).NewWriter(ctx)
	_, err = fmt.Fprint(wr, op.Name())
	if err != nil {
		return err
	}
	err = wr.Close()
	if err != nil {
		return err
	}

	log.Println("Sent audio recognition request with name", op.Name())

	return nil
}
