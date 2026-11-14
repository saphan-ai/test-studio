module github.com/saphan-ai/test-studio/actions

go 1.26

require (
	github.com/saphan-ai/test-studio/conformance v0.0.0
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/saphan-ai/test-studio/conformance => ../conformance
