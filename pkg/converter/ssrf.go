//go:build !integration

package converter

import (
	"net"
	"net/url"
)

// isPublicURL checks if a URL resolves to a public IP address to prevent SSRF attacks.
func (c *Converter) isPublicURL(urlStr string) (bool, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false, err
	}

	ips, err := net.LookupIP(parsedURL.Hostname())
	if err != nil {
		return false, err
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
			return false, nil // Found a non-public IP
		}
	}

	return true, nil
}
