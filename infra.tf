provider "google" {
  credentials = file("creds.json")
  project     = "patrickoyarzun"
  region      = "us-central1"
}

locals {
  project = random_string.project-suffix.result
}

resource "random_string" "project-suffix" {
  length  = 8
  special = false
  upper   = false
}

module "audio_bucket" {
  source = "./modules/private_bucket"

  name    = "audio"
  project = random_string.project-suffix.result
}

module "transcript_bucket" {
  source = "./modules/private_bucket"

  name    = "transcript"
  project = random_string.project-suffix.result
}

module "source_bucket" {
  source = "./modules/private_bucket"

  name    = "source"
  project = random_string.project-suffix.result
}

module "recognize_source" {
  source = "./modules/upload-source"

  artifact_name = "recognize"

  bucket     = module.source_bucket.bucket
  output_dir = "${path.module}/build"
  source_dir = "${path.module}/recognize"
}

module "track-progress_source" {
  source = "./modules/upload-source"

  artifact_name = "track-progress"

  bucket     = module.source_bucket.bucket
  output_dir = "${path.module}/build"
  source_dir = "${path.module}/track-progress"
}

resource "google_cloudfunctions_function" "recognize" {
  name        = "recognize"
  description = "Submit a long running recognition task for each audio sample"
  runtime     = "go111"

  available_memory_mb   = 128
  source_archive_bucket = module.source_bucket.bucket
  source_archive_object = module.recognize_source.bucket_path
  entry_point           = "RecognizeAudio"

  service_account_email = google_service_account.transcript-account.email

  event_trigger {
    event_type = "google.storage.object.finalize"
    resource   = module.audio_bucket.bucket
  }

  environment_variables = {
    PROGRESS_BUCKET = module.transcript_bucket.bucket
  }
}

resource "google_pubsub_topic" "track-progress-trigger" {
  name = "track-progress-trigger"
}

resource "google_pubsub_topic_iam_binding" "track-progress-publisher" {
  topic = google_pubsub_topic.track-progress-trigger.name

  role = "roles/pubsub.publisher"

  members = ["serviceAccount:${google_service_account.transcript-account.email}"]
}

resource "google_cloud_scheduler_job" "track-progress-trigger" {
  name     = "trigger-track-progress-job"
  description = "Trigger the track-progress function"
  schedule = "*/2 * * * *"

  pubsub_target {
    topic_name = google_pubsub_topic.track-progress-trigger.id
    data = "${base64encode("test")}"
  }
}

resource "google_cloudfunctions_function" "track-progress" {
  name        = "track-progress"
  description = "Check on the operation progress"
  runtime     = "go111"

  available_memory_mb   = 128
  source_archive_bucket = module.source_bucket.bucket
  source_archive_object = module.track-progress_source.bucket_path
  entry_point           = "TrackProgress"

  service_account_email = google_service_account.transcript-account.email

  event_trigger {
    event_type = "google.pubsub.topic.publish"
    resource   = google_pubsub_topic.track-progress-trigger.name
  }

  environment_variables = {
    PROGRESS_BUCKET = module.transcript_bucket.bucket
  }
}

resource "google_service_account" "transcript-account" {
  account_id   = "transcript-${local.project}"
  display_name = "Transcript Service Account"
}

resource "google_storage_bucket_iam_binding" "audio-admin" {
  bucket = module.audio_bucket.bucket

  role = "roles/storage.objectAdmin"

  members = ["serviceAccount:${google_service_account.transcript-account.email}"]
}

resource "google_storage_bucket_iam_binding" "transcript-admin" {
  bucket = module.transcript_bucket.bucket

  role = "roles/storage.objectAdmin"

  members = ["serviceAccount:${google_service_account.transcript-account.email}"]
}
