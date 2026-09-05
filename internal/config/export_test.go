package config

// IsNetworkFSName exposes the classification for testing.
//
// Exported here rather than in the package proper: it is an internal judgement,
// and the only reason to reach it from outside is to check that judgement
// against a table of filesystem names.
var IsNetworkFSName = isNetworkFSName
