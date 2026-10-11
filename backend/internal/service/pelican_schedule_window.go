package service

import (
	"fmt"
	"time"
)

var pelicanScheduleLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func validatePelicanWindow(cfg *PelicanTestConfig) error {
	if cfg == nil || (cfg.DailyStart == "" && cfg.DailyEnd == "") {
		return nil
	}
	for _, value := range []string{cfg.DailyStart, cfg.DailyEnd} {
		if _, err := time.Parse("15:04", value); err != nil || len(value) != 5 {
			return fmt.Errorf("daily window requires start and end in HH:mm format")
		}
	}
	if cfg.DailyStart == cfg.DailyEnd {
		return fmt.Errorf("daily window start and end must differ; disable the window for all-day testing")
	}
	return nil
}

// The start is inclusive and the end exclusive, including windows across midnight.
func pelicanWindowAllows(cfg *PelicanTestConfig, now time.Time) bool {
	if cfg == nil || (cfg.DailyStart == "" && cfg.DailyEnd == "") {
		return true
	}
	clock := now.In(pelicanScheduleLocation).Format("15:04")
	if cfg.DailyStart < cfg.DailyEnd {
		return clock >= cfg.DailyStart && clock < cfg.DailyEnd
	}
	return clock >= cfg.DailyStart || clock < cfg.DailyEnd
}

func computeNextPelicanRun(cronExpr string, from time.Time, cfg *PelicanTestConfig) (time.Time, error) {
	if err := validatePelicanWindow(cfg); err != nil {
		return time.Time{}, err
	}
	if cfg == nil || cfg.DailyStart == "" {
		return computeNextRun(cronExpr, from)
	}
	schedule, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	// Jump to each next window instead of checking every cron tick overnight.
	limit := from.AddDate(5, 0, 0)
	for next := schedule.Next(from.In(pelicanScheduleLocation)); !next.IsZero() && next.Before(limit); {
		if pelicanWindowAllows(cfg, next) {
			return next, nil
		}
		start, _ := time.Parse("15:04", cfg.DailyStart)
		local := next.In(pelicanScheduleLocation)
		opening := time.Date(local.Year(), local.Month(), local.Day(), start.Hour(), start.Minute(), 0, 0, pelicanScheduleLocation)
		if !opening.After(next) {
			opening = opening.AddDate(0, 0, 1)
		}
		next = schedule.Next(opening.Add(-time.Nanosecond))
	}
	return time.Time{}, fmt.Errorf("cron has no run inside the daily window")
}
