package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/FuzzyStatic/blizzard/v3"
	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
	"golang.org/x/time/rate"
)

func goDotEnvVariable(key string) string {
	err := godotenv.Load(".env")
	if err != nil {
		log.Printf("Error loading .env file, falling back to environment variables: %v", err)
	}
	return os.Getenv(key)
}

func init() {
	migrations.Register(func(app core.App) error {
		superusers, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
		if err != nil {
			return err
		}
		record := core.NewRecord(superusers)
		record.Set("email", goDotEnvVariable("PB_SUPERUSER_EMAIL"))
		record.Set("password", goDotEnvVariable("PB_SUPERUSER_PASSWORD"))
		app.Save(record)

		settings := app.Settings()
		settings.Meta.AppName = "Blitzbase"
		settings.Meta.AppURL = "http://127.0.0.1:8090"
		settings.Logs.MaxDays = 1
		settings.Logs.LogAuthId = false
		settings.Logs.LogIP = false
		settings.Meta.SenderAddress = "noreply@jrcloud.eu"
		settings.Meta.SenderName = "Blitzbase"
		settings.SMTP.Host = goDotEnvVariable("SMTP_HOST")
		port, err := strconv.Atoi(goDotEnvVariable("SMTP_PORT"))
		if err != nil {
			log.Fatalf("Invalid SMTP_PORT: %v", err)
		}
		settings.SMTP.Port = port
		settings.SMTP.Username = goDotEnvVariable("SMTP_USERNAME")
		settings.SMTP.Password = goDotEnvVariable("SMTP_PASSWORD")
		settings.RateLimits.Enabled = true
		app.Save(settings)

		collection, err := app.FindCollectionByNameOrId("characters")
		if err != nil {
			collection = core.NewBaseCollection("characters")
			collection.ViewRule = types.Pointer("")
			collection.ListRule = types.Pointer("")
		}

		if idField, ok := collection.Fields.GetByName("id").(*core.TextField); ok {
			idField.Min = 1
			idField.Max = 0
			idField.Pattern = "^[0-9]+$"
			idField.AutogeneratePattern = ""
		}

		addField := func(field core.Field) {
			if collection.Fields.GetByName(field.GetName()) == nil {
				collection.Fields.Add(field)
			}
		}
		addField(&core.TextField{Name: "name"})
		addField(&core.TextField{Name: "realm"})
		addField(&core.TextField{Name: "gender_type"})
		addField(&core.TextField{Name: "gender_name"})
		addField(&core.TextField{Name: "faction_type"})
		addField(&core.TextField{Name: "faction_name"})
		addField(&core.NumberField{Name: "race_id"})
		addField(&core.TextField{Name: "race_name"})
		addField(&core.NumberField{Name: "character_class_id"})
		addField(&core.TextField{Name: "character_class_name"})
		addField(&core.NumberField{Name: "active_spec_id"})
		addField(&core.TextField{Name: "active_spec_name"})
		addField(&core.TextField{Name: "realm_name"})
		addField(&core.NumberField{Name: "realm_id"})
		addField(&core.TextField{Name: "guild_name"})
		addField(&core.NumberField{Name: "guild_id"})
		addField(&core.TextField{Name: "guild_realm_name"})
		addField(&core.NumberField{Name: "guild_realm_id"})
		addField(&core.TextField{Name: "guild_realm_slug"})
		addField(&core.NumberField{Name: "level"})
		addField(&core.NumberField{Name: "experience"})
		addField(&core.NumberField{Name: "achievement_points"})
		addField(&core.NumberField{Name: "last_login_timestamp"})
		addField(&core.NumberField{Name: "average_item_level"})
		addField(&core.NumberField{Name: "equipped_item_level"})
		addField(&core.NumberField{Name: "active_title_id"})
		addField(&core.TextField{Name: "active_title_name"})
		addField(&core.TextField{Name: "active_title_display_string"})

		app.Save(collection)

		return nil
	}, func(app core.App) error { // optional revert operation
		record, _ := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, goDotEnvVariable("PB_SUPERUSER_EMAIL"))
		if record == nil {
			return nil // probably already deleted
		}

		return app.Delete(record)
	})
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

func normalizeValue(v any) string {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case float32:
		if n == float32(int32(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(float64(n), 'f', -1, 32)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func setRecordFields(record *core.Record, collection *core.Collection, fields map[string]any) {
	for name, value := range fields {
		if collection.Fields.GetByName(name) != nil {
			record.Set(name, value)
		}
	}
}
func blizzClient(app *pocketbase.PocketBase) {
	log.Printf("Starting update...")
	ctx := context.Background()
	throttledClient := http.DefaultClient
	throttledClient.Transport = NewThrottledTransport(time.Second/10, 100, http.DefaultTransport) // allows 10 requests every second //36000 per Hour
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
	roster, header, err := euBlizzClient.WoWGuildRoster(ctx, goDotEnvVariable("REALM_SLUG"), goDotEnvVariable("GUILD_SLUG"))
	if err != nil {
		log.Println(header)
		log.Println(err)
	}
	collection, err := app.FindCollectionByNameOrId("characters")
	if err != nil {
		log.Printf("Error finding collection: %v", err)
	}

	records, err := app.FindAllRecords("characters")
	if err != nil {
		log.Printf("Error finding records: %v", err)
		return
	}

	existingRecords := make(map[string]*core.Record, len(records))
	for _, record := range records {
		if record.Id != "" {
			existingRecords[record.Id] = record
		}
	}

	rosterKeys := make(map[string]struct{}, len(roster.Members))

	for _, member := range roster.Members {
		maxRetries := 3
		memberInfo, header, err := euBlizzClient.WoWCharacterProfileSummary(ctx, member.Character.Realm.Slug, member.Character.Name)
		for attempt := 1; attempt < maxRetries && header == nil; attempt++ {
			log.Printf("Attempt %d/%d: nil header for %s-%s, retrying...", attempt+1, maxRetries, member.Character.Name, member.Character.Realm.Slug)
			time.Sleep(time.Duration(attempt) * time.Second / 10)
			memberInfo, header, err = euBlizzClient.WoWCharacterProfileSummary(ctx, member.Character.Realm.Slug, member.Character.Name)
		}
		if err != nil {
			log.Println("Response Header:", header)
			log.Println(err)
			continue
		}
		if header == nil {
			log.Printf("Skipping %s-%s: nil header after %d retries", member.Character.Name, member.Character.Realm.Slug, maxRetries)
			continue
		}
		idValue := strconv.Itoa(memberInfo.ID)
		rosterKeys[idValue] = struct{}{}
		fieldValues := map[string]any{
			"name":                        memberInfo.Name,
			"realm":                       memberInfo.Realm.Slug,
			"realm_name":                  memberInfo.Realm.Name,
			"realm_id":                    memberInfo.Realm.ID,
			"gender_type":                 memberInfo.Gender.Type,
			"gender_name":                 memberInfo.Gender.Name,
			"faction_type":                memberInfo.Faction.Type,
			"faction_name":                memberInfo.Faction.Name,
			"race_id":                     memberInfo.Race.ID,
			"race_name":                   memberInfo.Race.Name,
			"character_class_id":          memberInfo.CharacterClass.ID,
			"character_class_name":        memberInfo.CharacterClass.Name,
			"active_spec_id":              memberInfo.ActiveSpec.ID,
			"active_spec_name":            memberInfo.ActiveSpec.Name,
			"guild_name":                  memberInfo.Guild.Name,
			"guild_id":                    memberInfo.Guild.ID,
			"guild_realm_name":            memberInfo.Guild.Realm.Name,
			"guild_realm_id":              memberInfo.Guild.Realm.ID,
			"guild_realm_slug":            memberInfo.Guild.Realm.Slug,
			"level":                       memberInfo.Level,
			"experience":                  memberInfo.Experience,
			"achievement_points":          memberInfo.AchievementPoints,
			"last_login_timestamp":        memberInfo.LastLoginTimestamp,
			"average_item_level":          memberInfo.AverageItemLevel,
			"equipped_item_level":         memberInfo.EquippedItemLevel,
			"active_title_id":             memberInfo.ActiveTitle.ID,
			"active_title_name":           memberInfo.ActiveTitle.Name,
			"active_title_display_string": memberInfo.ActiveTitle.DisplayString,
		}
		if record, ok := existingRecords[idValue]; ok {
			// check if any field value has changed, if not skip update
			same := true
			for key, value := range fieldValues {
				if normalizeValue(record.Get(key)) != normalizeValue(value) {
					same = false
					log.Printf("Field '%s' changed for %s-%s: '%v' -> '%v'", key, record.GetString("name"), record.GetString("realm_name"), normalizeValue(record.Get(key)), normalizeValue(value))
					break
				}
			}
			if same {
				//log.Printf("Skipping update for %s-%s, no changes detected.", record.GetString("name"), record.GetString("realm_name"))
				continue
			}
			setRecordFields(record, collection, fieldValues)
			err = app.Save(record)
			if err != nil {
				log.Printf("Error updating record for %s-%s: %v", memberInfo.Name, memberInfo.Realm.Name, err)
			} else {
				//log.Printf("Updated record for %s-%s", memberInfo.Name, memberInfo.Realm.Name)
			}
		} else {
			record := core.NewRecord(collection)
			record.Id = idValue
			setRecordFields(record, collection, fieldValues)
			err = app.Save(record)
			if err != nil {
				log.Printf("Error inserting record for %s-%s: %v", memberInfo.Name, memberInfo.Realm.Name, err)
			} else {
				//log.Printf("Inserted record for %s-%s", memberInfo.Name, memberInfo.Realm.Name)
			}
		}
	}
	log.Printf("Update finished with %d members.", len(roster.Members))
	log.Printf("Deleting old records...")
	for key, record := range existingRecords {
		if _, ok := rosterKeys[key]; !ok {
			err := app.Delete(record)
			if err != nil {
				log.Printf("Error deleting record: %v", err)
			} else {
				log.Printf("Deleted record for %s-%s", record.GetString("name"), record.GetString("realm_name"))
			}
		}
	}
	log.Printf("Update and Cleanup done.")
}

func main() {
	app := pocketbase.New()
	// runs the "Update" task every 7 minutes
	app.Cron().MustAdd("Update", "*/7 * * * *", func() {
		blizzClient(app)
	})

	// checks for new container image every 20 minutes (watchtower-like)
	app.Cron().MustAdd("SelfUpdate", "*/20 * * * *", func() {
		watchForUpdates()
	})
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		// serves static files from the provided public dir (if exists)
		se.Router.GET("/{path...}", apis.Static(os.DirFS("./pb_public"), false))

		return se.Next()
	})

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		total, err := app.CountRecords("characters")
		if total == 0 {
			log.Printf("No records found, starting initial update...")
			go blizzClient(app)
		} else if err != nil {
			log.Printf("Error counting records: %v", err)
		}
		return e.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
