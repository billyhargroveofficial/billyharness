// Package jobstore persists provider-neutral multi-agent jobs.
//
// The append-only JSONL event stream is canonical. Snapshots are disposable
// caches rebuilt by replaying jobs.Event values through jobs.Reduce.
//
// Security invariant: the store root and every ancestor below its trusted
// parent must be excluded from all worker write authorities. FileStore rejects
// final-component links and multiply-linked files and holds a process lock, but
// it is not a sandbox against an unrelated same-UID process that can rename its
// private directories. Runtime composition must preserve that boundary.
package jobstore
