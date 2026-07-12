// Package splunk executes SPL searches through the Splunk management REST API.
//
// Applications should construct a Client with NewClient, call Authenticate to
// fail fast on invalid credentials, and use Client.Search for job or export
// searches. Client is safe for concurrent searches after authentication.
// Search results are returned as the unmodified Splunk response body.
// Existing search jobs can be inspected, waited on, retrieved, diagnosed, or
// explicitly cancelled by SID. Waiting on an existing SID never cancels the
// remote job when the local context ends.
// Config.EventSink provides typed, non-sensitive lifecycle events. Delivery is
// synchronous and serialized per Client; sinks must return quickly.
//
// The package does not load environment variables, parse querysplunk YAML, or
// apply the CLI's deployment-impact safety policy. Callers must bound untrusted
// searches, protect returned event data, and avoid logging credentials or
// private Splunk URLs.
package splunk
