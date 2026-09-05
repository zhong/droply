package server

// CookieParentDomainForTest exposes cookieParentDomain for the external test package.
func CookieParentDomainForTest(callbackHost, originHost, baseDomain string) string {
	return cookieParentDomain(callbackHost, originHost, baseDomain)
}
