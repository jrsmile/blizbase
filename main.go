package main

import (
    "log"
    "os"
    "net/http"

    "github.com/pocketbase/pocketbase"
    "github.com/pocketbase/pocketbase/apis"
    "github.com/pocketbase/pocketbase/core"

    "github.com/FuzzyStatic/blizzard/v3"
)

func main() {
    usBlizzClient, err := blizzard.NewClient(blizzard.Config{
    ClientID:     "my_client_id",
    ClientSecret: "my_client_secret",
    HTTPClient:   http.DefaultClient,
    Region:       blizzard.EU,
    Locale:       blizzard.DeDE,
    })
    _ = usBlizzClient
    if err != nil {
        log.Fatal(err)
    }
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
