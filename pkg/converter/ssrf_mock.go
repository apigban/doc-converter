//go:build integration

package converter

// isPublicURL is a mock for testing purposes. It allows all URLs when the "integration" build tag is used.
func (c *Converter) isPublicURL(urlStr string) (bool, error) {
	return true, nil
}
