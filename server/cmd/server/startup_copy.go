package main

// Recovery copy is shared with the catalogue and checked by the server tests.
var startupCopy = map[string]string{
	"server.startup.failed":      "Portico could not start",
	"server.startup.phase":       "The server could not finish its startup checks.",
	"server.startup.recovery":    "Check the service log for details. Fix the cause, then restart Portico. Your files have not been removed.",
	"server.startup.storage":     "Check that the state folder is available, has free space, and can be read and written by the service.",
	"server.startup.version":     "This database does not match this Portico version. Keep a copy of the state folder, then restore a backup made with this version or start with a new state folder.",
	"server.startup.permissions": "Give the service account permission to read and write the state folder, then restart Portico.",
	"server.startup.space":       "Free space on the drive holding the state folder, then restart Portico.",
	"server.startup.instance":    "Stop the other Portico service using this state folder, then restart this service.",
}
