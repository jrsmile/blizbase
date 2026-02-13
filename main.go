package main

import (
    "log"
    "os"
    "net/http"
	"time"

	"golang.org/x/time/rate"
    "github.com/pocketbase/pocketbase"
    "github.com/pocketbase/pocketbase/apis"
    "github.com/pocketbase/pocketbase/core"
    "github.com/FuzzyStatic/blizzard/v3"

)

type ThrottledTransport struct {
	roundTripperWrap http.RoundTripper
	ratelimiter      *rate.Limiter
}

func (c *ThrottledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	err := c.ratelimiter.Wait(r.Context()) // This is a blocking call. Honors the rate limit
	if err != nil {
		return nil, err
	}
	return c.roundTripperWrap.RoundTrip(r)
}

func NewThrottledTransport(limitPeriod time.Duration, requestCount int, transportWrap http.RoundTripper) http.RoundTripper {
	return &ThrottledTransport{
		roundTripperWrap: transportWrap,
		ratelimiter:      rate.NewLimiter(rate.Every(limitPeriod), requestCount),
	}
}
func blizzClientExample() {
    client := http.DefaultClient
    client.Transport = NewThrottledTransport(10*time.Second, 60, http.DefaultTransport) // allows 60 requests every 10 seconds
    euBlizzClient, err := blizzard.NewClient(blizzard.Config{
    ClientID:     "my_client_id",
    ClientSecret: "my_client_secret",
    HTTPClient:   client,
    Region:       blizzard.EU,
    Locale:       blizzard.DeDE,
    })
    _ = euBlizzClient
    if err != nil {
        log.Fatal(err)
    }
}

func main() {
    blizzClientExample()
    app := pocketbase.New()

    app.OnServe().BindFunc(func(se *core.ServeEvent) error {
        // serves static files from the provided public dir (if exists)
        se.Router.GET("/{path...}", apis.Static(os.DirFS("./pb_public"), false))

        return se.Next()
    })

    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
