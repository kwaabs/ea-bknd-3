package salessummary

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"bknd-3/internal/botconsumption"
	"bknd-3/internal/bxcconsumption"
	"bknd-3/internal/mmssales"
	"bknd-3/internal/zeusbilling"

	"github.com/uptrace/bun"
)

type Service struct {
	zeus *zeusbilling.Service
	mms  *mmssales.Service
	bot  *botconsumption.Service
	bxc  *bxcconsumption.Service
}

func NewService(db *bun.DB) *Service {
	return &Service{
		zeus: zeusbilling.NewService(db),
		mms:  mmssales.NewService(db),
		bot:  botconsumption.NewService(db),
		bxc:  bxcconsumption.NewService(db),
	}
}

// sourceFetcher fetches one source's normalized rows for the requested
// filters and grouping dimension ("region" or "district").
type sourceFetcher func(ctx context.Context, f CommonFilters, groupBy string) ([]normalizedRow, error)

// sourcesFor is the one place a new source gets wired in. Every consumer
// of Summary() picks up a newly-added source automatically — nothing else
// in this package, or in any frontend file, needs to change.
//
// Zeus Postpaid and Zeus AMR are listed as two separate sources (not
// combined) even though every existing Postpaid-total consumer treats them
// as one bucket — combining them here would lose information a caller
// might want (the dashboard's KPI badges already show them separately).
// Callers that want a combined Postpaid figure just sum both entries from
// BySource; nothing forces every caller to agree on that grouping choice.
func (s *Service) sourcesFor(category Category) (map[string]sourceFetcher, error) {
	switch category {
	case Prepaid:
		return map[string]sourceFetcher{
			// MMS takes precedence over Zeus Prepaid on any meter it
			// already has — the one real overlap in this system (confirmed
			// against production data). Every other pairing here (BOT vs
			// Zeus/MMS, BXC vs Zeus/MMS, BOT vs BXC) is confirmed genuinely
			// unique, so those sum in directly with no precedence logic.
			"zeus_prepaid": s.zeusRows("Prepaid", true),
			"mms":          s.mmsRows,
			"bot":          s.botRows,
			"bxc":          s.bxcRows,
			// PNS has no backend yet — add it here once it does.
		}, nil
	case Postpaid:
		return map[string]sourceFetcher{
			"zeus_postpaid": s.zeusRows("Postpaid", false),
			"zeus_amr":      s.zeusRows("AMR", false),
		}, nil
	default:
		return nil, fmt.Errorf("unknown category %q", category)
	}
}

func (s *Service) zeusRows(meterModelType string, excludeMmsDuplicates bool) sourceFetcher {
	return func(ctx context.Context, f CommonFilters, groupBy string) ([]normalizedRow, error) {
		gb := "regionname"
		if groupBy == "district" {
			gb = "districtname"
		}
		res, err := s.zeus.Aggregate(ctx, zeusbilling.FilterParams{
			RegionName:     f.Region,
			DistrictName:   f.District,
			MeterModelType: []string{meterModelType},
			BillDateFrom:   f.DateFrom,
			BillDateTo:     f.DateTo,
		}, []string{gb}, excludeMmsDuplicates)
		if err != nil {
			return nil, err
		}
		out := make([]normalizedRow, len(res.Data))
		for i, r := range res.Data {
			val := r.RegionName
			if groupBy == "district" {
				val = r.DistrictName
			}
			out[i] = normalizedRow{GroupValue: val, Kwh: r.SumBillConsumptionValue, Customers: r.CustomerCount}
		}
		return out, nil
	}
}

func (s *Service) mmsRows(ctx context.Context, f CommonFilters, groupBy string) ([]normalizedRow, error) {
	gb := "region"
	if groupBy == "district" {
		gb = "district"
	}
	res, err := s.mms.Aggregate(ctx, mmssales.FilterParams{
		Region:       f.Region,
		District:     f.District,
		DateTimeFrom: f.DateFrom,
		DateTimeTo:   f.DateTo,
	}, []string{gb})
	if err != nil {
		return nil, err
	}
	out := make([]normalizedRow, len(res.Data))
	for i, r := range res.Data {
		val := r.Region
		if groupBy == "district" {
			val = r.District
		}
		out[i] = normalizedRow{GroupValue: val, Kwh: r.SumLastMonthKwhRead, Customers: r.CustomerCount}
	}
	return out, nil
}

func (s *Service) botRows(ctx context.Context, f CommonFilters, groupBy string) ([]normalizedRow, error) {
	res, err := s.bot.Aggregate(ctx, botconsumption.FilterParams{
		Region:   f.Region,
		District: f.District,
		DateFrom: f.DateFrom,
		DateTo:   f.DateTo,
	}, []string{groupBy})
	if err != nil {
		return nil, err
	}
	out := make([]normalizedRow, len(res.Data))
	for i, r := range res.Data {
		val := r.Region
		if groupBy == "district" {
			val = r.District
		}
		out[i] = normalizedRow{GroupValue: val, Kwh: r.SumKwh, Customers: r.CustomerCount}
	}
	return out, nil
}

func (s *Service) bxcRows(ctx context.Context, f CommonFilters, groupBy string) ([]normalizedRow, error) {
	res, err := s.bxc.Aggregate(ctx, bxcconsumption.FilterParams{
		Region:   f.Region,
		District: f.District,
		DateFrom: f.DateFrom,
		DateTo:   f.DateTo,
	}, []string{groupBy})
	if err != nil {
		return nil, err
	}
	out := make([]normalizedRow, len(res.Data))
	for i, r := range res.Data {
		val := r.Region
		if groupBy == "district" {
			val = r.District
		}
		out[i] = normalizedRow{GroupValue: val, Kwh: r.SumKwh, Customers: r.CustomerCount}
	}
	return out, nil
}

// suffixPattern strips a trailing "Region" or "District" administrative-
// unit qualifier — the recurring naming mismatch this whole system has
// (Zeus stores "Tema Region"/"Cape Coast District", MMS/BOT/BXC store the
// short form). Used as the MERGE KEY across sources here so the same real
// region/district lands in one Row instead of splitting into two — the
// server-side equivalent of the frontend's normalizeRegionName/
// shortRegionLabel (use-resolved-region-name.ts), now canonical in one
// place instead of needing to be re-applied by every consumer.
var suffixPattern = regexp.MustCompile(`(?i)\s+(region|district)$`)

func shortLabel(raw string) string {
	return strings.TrimSpace(suffixPattern.ReplaceAllString(strings.TrimSpace(raw), ""))
}

func normalizeGroupKey(raw string) string {
	return strings.ToLower(shortLabel(raw))
}

// Summary fetches every source registered for category, merges them by
// region or district (whichever groupBy asks for — anything other than
// "district" is treated as "region"), and returns one cross-source total.
// Sources are fetched concurrently since they're independent queries.
func (s *Service) Summary(ctx context.Context, category Category, f CommonFilters, groupBy string) (*Summary, error) {
	if groupBy != "district" {
		groupBy = "region"
	}

	sources, err := s.sourcesFor(category)
	if err != nil {
		return nil, err
	}

	type fetchResult struct {
		name string
		rows []normalizedRow
		err  error
	}
	results := make(chan fetchResult, len(sources))
	var wg sync.WaitGroup
	for name, fetch := range sources {
		wg.Add(1)
		go func(name string, fetch sourceFetcher) {
			defer wg.Done()
			rows, err := fetch(ctx, f, groupBy)
			results <- fetchResult{name: name, rows: rows, err: err}
		}(name, fetch)
	}
	wg.Wait()
	close(results)

	rowsByKey := make(map[string]*Row)
	bySource := make(map[string]SourceStat)
	var totalKwh float64
	var totalCustomers int64

	for res := range results {
		if res.err != nil {
			return nil, fmt.Errorf("%s: %w", res.name, res.err)
		}
		srcStat := bySource[res.name]
		for _, r := range res.rows {
			if r.GroupValue == "" {
				r.GroupValue = "Unknown"
			}
			key := normalizeGroupKey(r.GroupValue)
			row, ok := rowsByKey[key]
			if !ok {
				row = &Row{GroupValue: shortLabel(r.GroupValue), BySource: map[string]SourceStat{}}
				rowsByKey[key] = row
			}
			stat := row.BySource[res.name]
			stat.Kwh += r.Kwh
			stat.Customers += r.Customers
			row.BySource[res.name] = stat
			row.TotalKwh += r.Kwh
			row.TotalCustomers += r.Customers

			srcStat.Kwh += r.Kwh
			srcStat.Customers += r.Customers
			totalKwh += r.Kwh
			totalCustomers += r.Customers
		}
		bySource[res.name] = srcStat
	}

	rows := make([]Row, 0, len(rowsByKey))
	for _, row := range rowsByKey {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TotalKwh > rows[j].TotalKwh })

	return &Summary{
		TotalKwh:       totalKwh,
		TotalCustomers: totalCustomers,
		BySource:       bySource,
		Rows:           rows,
	}, nil
}
