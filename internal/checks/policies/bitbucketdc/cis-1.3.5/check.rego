package scmbench.rules.cis_1_3_5

import rego.v1

# Bitbucket Data Center delegates authentication to an external identity
# provider (Crowd, SAML SSO, LDAP), so multi-factor enforcement lives there and
# is not observable through Bitbucket's REST API at all. Reporting PASS or FAIL
# from Bitbucket data would be a fabrication either way.
result := {
	"status": "MANUAL",
	"details": "Multi-factor authentication is enforced by the identity provider in front of Bitbucket Data Center, not by Bitbucket, and cannot be read through its API. Verify enforcement in your SSO, Crowd or LDAP configuration.",
}
