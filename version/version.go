package version

var (
	Version    = "0.1.1"
	SDKVersion = "1.21.0"
)

// TestGofmtViolation is a deliberately misformatted function
// to trigger gofmt detection in the CI monitor test harness.
func  TestGofmtViolation()  string  {
  x:=    "ci-monitor-test"
  return   x
}
