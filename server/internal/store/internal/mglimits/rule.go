// Package mglimits holds shared MG validation limits without dependencies.
package mglimits

// RuleConfigBytes is the existing 384 KiB serialized rule configuration ceiling.
// This is validation, not a resource reservation; consumers reserve actual bytes.
const RuleConfigBytes = 384 << 10
