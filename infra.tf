locals {
  project = random_string.project-suffix.result

  transcriptServiceAccount = google_service_account.transcript-account.email
  appEngineServiceAccount  = "${data.google_project.project.project_id}@appspot.gserviceaccount.com"
  appEngineBucket          = "${data.google_project.project.project_id}.appspot.com"

  services = [
    "pubsub.googleapis.com",
    "iam.googleapis.com",
    "appengine.googleapis.com",
    "cloudfunctions.googleapis.com",
    "cloudscheduler.googleapis.com",
    "speech.googleapis.com",
    "iap.googleapis.com",
  ]

  serviceAccountRoles = [
    "roles/editor",                         // TODO: restrict scope
    "roles/storage.admin",                  // lots of storage manipulation, admin is probably not necessary
    "roles/iam.serviceAccountTokenCreator", // Used to sign presigned urls
  ]
}

terraform {
  backend "gcs" {
    bucket = "tf-state-root"
    prefix = "terraform/state"
  }
  required_providers {
    google = "~> 3.31.0"
  }
}

provider "google" {
  region = "us-central1"
  zone   = "us-central1-c"
}

variable "owner_email" {
}

data "google_project" "project" {
}

resource "google_project_iam_member" "service-account-roles" {
  count  = length(local.serviceAccountRoles)
  role   = local.serviceAccountRoles[count.index]
  member = "serviceAccount:${local.transcriptServiceAccount}"
}

// These restrict scope to a specific resource
resource "google_pubsub_topic_iam_binding" "track-progress-publisher" {
  topic = google_pubsub_topic.track-progress-trigger.name

  role = "roles/pubsub.publisher"

  members = ["serviceAccount:${local.transcriptServiceAccount}"]
}

resource "google_service_account_iam_binding" "admin-account-iam" {
  service_account_id = google_service_account.transcript-account.name
  role               = "roles/iam.serviceAccountTokenCreator"

  members = ["serviceAccount:${local.transcriptServiceAccount}"]
}


resource "google_project_iam_member" "appengine_storage_admin" {
  count  = length(local.serviceAccountRoles)
  role   = local.serviceAccountRoles[count.index]
  member = "serviceAccount:${local.appEngineServiceAccount}"
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

module "source_bucket" {
  source = "./modules/private_bucket"

  name    = "source"
  project = random_string.project-suffix.result
}

module "frontend_source" {
  source = "./modules/upload-source"

  artifact_name = "frontend"

  bucket     = module.source_bucket.bucket
  output_dir = "${path.module}/build"
  source_dir = "${path.module}/frontend"
}

module "convert_source" {
  source = "./modules/upload-source"

  artifact_name = "convert"

  bucket     = module.source_bucket.bucket
  output_dir = "${path.module}/build"
  source_dir = "${path.module}/convert"
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

resource "google_cloudfunctions_function" "convert" {
  name        = "convert"
  description = "Convert audio samples to mono channel FLAC"
  runtime     = "nodejs8"

  available_memory_mb   = 1024
  source_archive_bucket = module.source_bucket.bucket
  source_archive_object = module.convert_source.bucket_path
  entry_point           = "ConvertAudio"

  service_account_email = local.transcriptServiceAccount

  event_trigger {
    event_type = "google.storage.object.finalize"
    resource   = module.audio_bucket.bucket
  }
}

resource "google_cloudfunctions_function" "recognize" {
  name        = "recognize"
  description = "Submit a long running recognition task for each audio sample"
  runtime     = "go111"

  available_memory_mb   = 128
  source_archive_bucket = module.source_bucket.bucket
  source_archive_object = module.recognize_source.bucket_path
  entry_point           = "RecognizeAudio"

  service_account_email = local.transcriptServiceAccount

  event_trigger {
    event_type = "google.storage.object.finalize"
    resource   = module.audio_bucket.bucket
  }
}

resource "google_pubsub_topic" "track-progress-trigger" {
  name = "track-progress-trigger"
}


resource "google_cloud_scheduler_job" "track-progress-trigger" {
  name        = "trigger-track-progress-job"
  description = "Trigger the track-progress function"
  schedule    = "*/2 * * * *"

  pubsub_target {
    topic_name = google_pubsub_topic.track-progress-trigger.id
    data       = base64encode("test")
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

  service_account_email = local.transcriptServiceAccount

  event_trigger {
    event_type = "google.pubsub.topic.publish"
    resource   = google_pubsub_topic.track-progress-trigger.name
  }

  environment_variables = {
    PROGRESS_BUCKET = module.audio_bucket.bucket
  }
}

resource "google_service_account" "transcript-account" {
  account_id   = "transcript-${local.project}"
  display_name = "Transcript Service Account"
}

resource "google_app_engine_application" "app" {
  project     = data.google_project.project.project_id
  location_id = "us-central"

  lifecycle {
    ignore_changes = [
      iap,
    ]
  }
}

resource "google_app_engine_standard_app_version" "frontend_primary" {
  version_id = "primary"
  service    = "default"
  runtime    = "go113"

  deployment {
    zip {
      source_url = "https://storage.googleapis.com/${module.source_bucket.bucket}/${module.frontend_source.bucket_path}"
    }
  }

  env_variables = {
    UPLOADABLE_BUCKET    = module.audio_bucket.bucket
    SERVICE_ACCOUNT      = local.transcriptServiceAccount
    GOOGLE_CLOUD_PROJECT = data.google_project.project.name
  }
}

resource "google_iap_app_engine_service_iam_member" "member" {
  project = google_app_engine_standard_app_version.frontend_primary.project
  app_id  = google_app_engine_standard_app_version.frontend_primary.project
  service = google_app_engine_standard_app_version.frontend_primary.service
  role    = "roles/iap.httpsResourceAccessor"
  member  = "user:${var.owner_email}"
}

resource "google_project_service" "project" {
  count   = length(local.services)
  service = local.services[count.index]

  disable_dependent_services = true
}
