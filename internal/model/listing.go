package model

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 2

type Listing struct {
	SchemaVersion int            `json:"schema_version"`
	Source        Source         `json:"source"`
	Location      Location       `json:"location"`
	Property      Property       `json:"property"`
	Broker        Broker         `json:"broker,omitempty"`
	Images        []Asset        `json:"images"`
	Videos        []Asset        `json:"videos,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Warnings      []string       `json:"warnings"`
}

type Source struct {
	URL              string    `json:"url"`
	CanonicalURL     string    `json:"canonical_url,omitempty"`
	Site             string    `json:"site"`
	ListingID        string    `json:"listing_id,omitempty"`
	ListingReference string    `json:"listing_reference,omitempty"`
	FirstListed      string    `json:"first_listed,omitempty"`
	LastUpdated      string    `json:"last_updated,omitempty"`
	RetrievedAt      time.Time `json:"retrieved_at"`
}

type Location struct {
	Address      string   `json:"address,omitempty"`
	Street       string   `json:"street,omitempty"`
	Municipality string   `json:"municipality,omitempty"`
	Region       string   `json:"region,omitempty"`
	PostalCode   string   `json:"postal_code,omitempty"`
	Country      string   `json:"country,omitempty"`
	MapURL       string   `json:"map_url,omitempty"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
}

type Property struct {
	Title        string       `json:"title,omitempty"`
	Type         string       `json:"type,omitempty"`
	Availability string       `json:"availability,omitempty"`
	Price        *Money       `json:"price,omitempty"`
	PricePerArea *UnitPrice   `json:"price_per_area,omitempty"`
	Bedrooms     *float64     `json:"bedrooms,omitempty"`
	Bathrooms    *float64     `json:"bathrooms,omitempty"`
	Floors       *float64     `json:"floors,omitempty"`
	InteriorArea *Measurement `json:"interior_area,omitempty"`
	LotArea      *Measurement `json:"lot_area,omitempty"`
	YearBuilt    *int         `json:"year_built,omitempty"`
	PhotoCount   *int         `json:"photo_count,omitempty"`
	VideoURL     string       `json:"video_url,omitempty"`
	Description  string       `json:"description,omitempty"`
	Features     []string     `json:"features"`
}

type Money struct {
	Amount   *float64 `json:"amount"`
	Currency string   `json:"currency,omitempty"`
	Display  string   `json:"display,omitempty"`
}

type Measurement struct {
	Value   *float64 `json:"value"`
	Unit    string   `json:"unit,omitempty"`
	Display string   `json:"display,omitempty"`
}

type UnitPrice struct {
	Amount   *float64 `json:"amount"`
	Currency string   `json:"currency,omitempty"`
	PerUnit  string   `json:"per_unit,omitempty"`
	Display  string   `json:"display,omitempty"`
}

type Broker struct {
	Agent            string `json:"agent,omitempty"`
	AgentProfileURL  string `json:"agent_profile_url,omitempty"`
	AgentLicense     string `json:"agent_license,omitempty"`
	Agency           string `json:"agency,omitempty"`
	AgencyProfileURL string `json:"agency_profile_url,omitempty"`
	AgencyAddress    string `json:"agency_address,omitempty"`
}

type Asset struct {
	SourceURL       string `json:"source_url"`
	File            string `json:"file,omitempty"`
	MediaType       string `json:"media_type,omitempty"`
	Bytes           int64  `json:"bytes,omitempty"`
	Status          string `json:"status,omitempty"`
	PosterSourceURL string `json:"poster_source_url,omitempty"`
	PosterFile      string `json:"poster_file,omitempty"`
	Error           string `json:"error,omitempty"`
}

func New(sourceURL, site string, retrievedAt time.Time) Listing {
	return Listing{
		SchemaVersion: SchemaVersion,
		Source:        Source{URL: sourceURL, Site: site, RetrievedAt: retrievedAt.UTC()},
		Property:      Property{Features: []string{}},
		Images:        []Asset{},
		Videos:        []Asset{},
		Warnings:      []string{},
	}
}

func CloneMetadata(values []json.RawMessage) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make([]any, 0, len(values))
	for _, raw := range values {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			out = append(out, value)
		}
	}
	return map[string]any{"json_ld": out}
}
