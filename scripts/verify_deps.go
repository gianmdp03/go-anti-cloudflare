package main

import (
	"fmt"
	"runtime"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

func main() {
	fmt.Printf("[OK] Go Runtime Version: %s\n", runtime.Version())
	fmt.Printf("[OK] OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)

	// Verify tls-client initialization
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(15),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithNotFollowRedirects(),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize tls-client: %v", err))
	}
	_ = client

	fmt.Println("[OK] tls-client v1.16.0 loaded and initialized successfully with profile Chrome_131.")
}
