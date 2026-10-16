// Package router serves a role from its endpoint group: ordered failover across
// endpoint-model pairs, bounded tries and total time, and per-endpoint health marking.
// It never fails over across roles.
package router
