package api

type ValidatorEpochStats struct {
	Stats struct {
		Config struct {
			MinVersion string `json:"min_version"`
		} `json:"config"`
	} `json:"stats"`
}
