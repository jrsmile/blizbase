package main

import (
    "log"
    "os"
    "net/http"
	"time"
    "context"

	"golang.org/x/time/rate"
    "github.com/joho/godotenv"
    "github.com/pocketbase/pocketbase"
    "github.com/pocketbase/pocketbase/apis"
    "github.com/pocketbase/pocketbase/core"
    "github.com/FuzzyStatic/blizzard/v3"

)

func goDotEnvVariable(key string) string {
  err := godotenv.Load(".env")
  if err != nil {
    log.Fatalf("Error loading .env file")
  }
  return os.Getenv(key)
}

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
    ctx := context.Background()
    throttledClient := http.DefaultClient
    throttledClient.Transport = NewThrottledTransport(time.Second/10, 10, http.DefaultTransport) // allows 10 requests every second //36000 per Hour
    euBlizzClient, err := blizzard.NewClient(blizzard.Config{
    ClientID:     goDotEnvVariable("CLIENT_ID"),
    ClientSecret: goDotEnvVariable("CLIENT_SECRET"),
    HTTPClient:   throttledClient,
    Region:       blizzard.EU,
    Locale:       blizzard.DeDE,
    })
    err = euBlizzClient.AccessTokenRequest(ctx)
    if err != nil {
    log.Println(err)
    }
    roster , header, err:= euBlizzClient.WoWGuildRoster(ctx, goDotEnvVariable("REALM_SLUG"),goDotEnvVariable("GUILD_SLUG"))
    if err != nil {
        log.Println(header)
        log.Println(err)
    }
    for _, member := range roster.Members {
        memberInfo, header, err := euBlizzClient.WoWCharacterProfileSummary(ctx, member.Character.Realm.Slug, member.Character.Name)
        if err != nil {
            log.Println("Response Header:", header)
            log.Println(err)
        }
        log.Printf("Member: %s, Server: %s, Level: %d, Points: %d\n", memberInfo.Name, memberInfo.Realm.Slug, memberInfo.Level, memberInfo.AchievementPoints)
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
