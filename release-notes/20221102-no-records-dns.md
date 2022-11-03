<!-- markdownlint-disable MD041 -->
Service Discovery now returns a DNS error message in the response body when no matching records are found for the query to
"clusterset.local". This prevents unnecessary retries.
