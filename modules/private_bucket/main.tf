variable "project" {}
variable "name" {}

resource "google_storage_bucket" "bucket" {
  name     = "${var.name}-${var.project}"
  location = "US"

   cors {
     max_age_seconds = 3600
     method          = ["PUT"]
     origin          = ["*"]
     response_header = ["Content-Type"]
   }
}

resource "google_storage_bucket_acl" "bucket-acl" {
  bucket = google_storage_bucket.bucket.name

  predefined_acl = "projectPrivate"
}

output "bucket" {
  value = google_storage_bucket.bucket.name
}
