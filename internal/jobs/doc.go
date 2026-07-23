// Package jobs defines the pure, provider-neutral domain model for durable
// multi-agent jobs.
//
// A job is advanced through bounded, flat worker batches. Provider calls,
// persistence, scheduling, gateway transport, and goroutine ownership live
// outside this package.
package jobs
