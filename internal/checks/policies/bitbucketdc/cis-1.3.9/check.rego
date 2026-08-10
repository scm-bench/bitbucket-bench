package scmbench.rules.cis_1_3_9

import rego.v1

# Domain verification badges are a hosted-SaaS concept with no counterpart in a
# self-hosted Bitbucket Data Center instance. The control is carried here as an
# explicit NA so that a reader can see it was considered and dismissed, rather
# than wondering why the benchmark numbering skips it.
result := {
	"status": "NA",
	"details": "Organization domain verification is specific to hosted SCM platforms and has no equivalent on a self-hosted Bitbucket Data Center instance.",
}
