# refaudit

`refaudit` finds public functions and types in a repository (like this one) that are not used in other repositories. It was made to answer this question:

> **What is in this package that is exported but not used?**

## Usage

1. Install with `go install github.com/launchdarkly-labs/refaudit@latest`.
2. Clone all the repos you need to audit.
3. Run this tool with `refaudit`. Run it without args to get usage info.
