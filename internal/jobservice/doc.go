// Package jobservice owns background execution of durable multi-agent jobs.
//
// A Manager is deliberately thinner than the durable runtime. It admits at
// most one local Step loop per job, keeps that loop alive independently of an
// HTTP request, and annotates durable store views with process-local activity
// and last-error information. Job state remains owned by jobstore; the
// process-local annotations are never replay inputs.
package jobservice
