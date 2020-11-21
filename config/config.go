package config

// AppVersion is the version string type
type AppVersion string

// GetVersion provides the current version of the project
func GetVersion() AppVersion {
	return "0.1-dev"
}
