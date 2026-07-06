# A complete list of services that form a part of Google Cloud. ⭐⭐⭐

**Source:** Google Cloud Terms of Service + Gemini API Terms
**URL:** https://cloud.google.com/archive/terms/services-20250408?hl=de

## Summary
VPC Service Controls allows administrators to configure security perimeters for API-based services including Cloud Storage (via `storage.googleapis.com` API), BigQuery (via `bigquery.googleapis.com` API), and Cloud Bigtable (via `bigtable.googleapis.com` API).. Perimeters enforce access restrictions based on * authorized VPC networks only*, blocking access from non-VPC or untrusted sources (e.g., internet, other clouds).. The feature mitigates data exfiltration by preventing API calls from outside the perimeter, even if credentials (e.g., service account keys) are compromised..

## Key Findings
- VPC Service Controls allows administrators to configure security perimeters for API-based services including Cloud Storage (via `storage.googleapis.com` API), BigQuery (via `bigquery.googleapis.com` API), and Cloud Bigtable (via `bigtable.googleapis.com` API).
- Perimeters enforce access restrictions based on * authorized VPC networks only*, blocking access from non-VPC or untrusted sources (e.g., internet, other clouds).
- The feature mitigates data exfiltration by preventing API calls from outside the perimeter, even if credentials (e.g., service account keys) are compromised.

**Relevance:** 3/5 | **Impact:** medium

---

