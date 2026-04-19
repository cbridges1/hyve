package shared

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reference time: Sunday 2026-04-19 10:00:00 UTC
var cronRef = time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

// ── CronNextOccurrence ────────────────────────────────────────────────────────

func TestCronNextOccurrence_WildcardAll(t *testing.T) {
	// * * * * * → next minute
	next, err := CronNextOccurrence("* * * * *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, cronRef.Add(time.Minute).Truncate(time.Minute), next)
}

func TestCronNextOccurrence_DailyMidnight(t *testing.T) {
	// 0 0 * * * → next midnight (Mon Apr 20)
	next, err := CronNextOccurrence("0 0 * * *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_SpecificHourAndMinute(t *testing.T) {
	// 30 14 * * * → today at 14:30 (after current 10:00)
	next, err := CronNextOccurrence("30 14 * * *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 19, 14, 30, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_HourAlreadyPassed(t *testing.T) {
	// 0 8 * * * → 08:00 is before 10:00, so next is tomorrow
	next, err := CronNextOccurrence("0 8 * * *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_SpecificWeekday(t *testing.T) {
	// 0 20 * * 5 → next Friday (Apr 24) at 20:00; ref is Sunday Apr 19
	next, err := CronNextOccurrence("0 20 * * 5", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 24, 20, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_WeekdayRange(t *testing.T) {
	// 0 18 * * 1-5 → weekdays at 18:00; ref is Sunday, next is Monday Apr 20
	next, err := CronNextOccurrence("0 18 * * 1-5", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 18, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_WeekdayList(t *testing.T) {
	// 0 9 * * 1,3,5 → Mon/Wed/Fri at 09:00; ref is Sunday, next is Monday Apr 20
	next, err := CronNextOccurrence("0 9 * * 1,3,5", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_SpecificDayOfMonth(t *testing.T) {
	// 0 0 1 * * → 1st of next month (May 1) since we're on Apr 19
	next, err := CronNextOccurrence("0 0 1 * *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_SpecificMonth(t *testing.T) {
	// 0 0 1 6 * → Jun 1 at midnight
	next, err := CronNextOccurrence("0 0 1 6 *", cronRef)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), next)
}

func TestCronNextOccurrence_NeverMatchesSameMinute(t *testing.T) {
	// Result must always be strictly after `from`
	ref := time.Date(2026, 4, 19, 20, 0, 0, 0, time.UTC)
	next, err := CronNextOccurrence("0 20 * * *", ref)
	require.NoError(t, err)
	assert.True(t, next.After(ref))
}

func TestCronNextOccurrence_InvalidFieldCount(t *testing.T) {
	_, err := CronNextOccurrence("0 0 *", cronRef)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "5 fields")
}

func TestCronNextOccurrence_InvalidFieldValue(t *testing.T) {
	_, err := CronNextOccurrence("abc 0 * * *", cronRef)
	require.Error(t, err)
}

func TestCronNextOccurrence_InvalidRange(t *testing.T) {
	_, err := CronNextOccurrence("0 0 * * a-b", cronRef)
	require.Error(t, err)
}

// ── validateCronExpr ─────────────────────────────────────────────────────────

func TestValidateCronExpr_Valid(t *testing.T) {
	for _, expr := range []string{
		"* * * * *",
		"0 0 * * *",
		"0 20 * * 5",
		"30 14 1 6 *",
		"0 18 * * 1-5",
		"0 9 * * 1,3,5",
	} {
		assert.NoError(t, validateCronExpr(expr), "expected %q to be valid", expr)
	}
}

func TestValidateCronExpr_Empty(t *testing.T) {
	assert.Error(t, validateCronExpr(""))
	assert.Error(t, validateCronExpr("   "))
}

func TestValidateCronExpr_WrongFieldCount(t *testing.T) {
	assert.Error(t, validateCronExpr("0 0 *"))
	assert.Error(t, validateCronExpr("0 0 * * * *"))
}

func TestValidateCronExpr_InvalidValue(t *testing.T) {
	assert.Error(t, validateCronExpr("abc 0 * * *"))
	assert.Error(t, validateCronExpr("0 0 * * a-b"))
}

// ── newCronPickerModel ────────────────────────────────────────────────────────

func TestNewCronPickerModel_EmptyString(t *testing.T) {
	m := newCronPickerModel("")
	for i, v := range m.values {
		assert.Equal(t, -1, v, "field %d should default to -1 (any)", i)
	}
	assert.Equal(t, 0, m.focused)
}

func TestNewCronPickerModel_ValidExpression(t *testing.T) {
	m := newCronPickerModel("30 20 15 6 5")
	assert.Equal(t, 30, m.values[cronFieldMinute])
	assert.Equal(t, 20, m.values[cronFieldHour])
	assert.Equal(t, 15, m.values[cronFieldDayOfMonth])
	assert.Equal(t, 6, m.values[cronFieldMonth])
	assert.Equal(t, 5, m.values[cronFieldDayOfWeek])
}

func TestNewCronPickerModel_WildcardsInExpression(t *testing.T) {
	m := newCronPickerModel("0 20 * * 5")
	assert.Equal(t, 0, m.values[cronFieldMinute])
	assert.Equal(t, 20, m.values[cronFieldHour])
	assert.Equal(t, -1, m.values[cronFieldDayOfMonth])
	assert.Equal(t, -1, m.values[cronFieldMonth])
	assert.Equal(t, 5, m.values[cronFieldDayOfWeek])
}

func TestNewCronPickerModel_InvalidExpressionDefaultsAll(t *testing.T) {
	// Wrong field count → all fields stay at -1
	m := newCronPickerModel("0 0 *")
	for i, v := range m.values {
		assert.Equal(t, -1, v, "field %d should be -1 on parse failure", i)
	}
}

// ── cronPickerModel.cronStep ──────────────────────────────────────────────────

func TestCronStep_UpFromAny_GoesToMin(t *testing.T) {
	m := newCronPickerModel("")
	m.focused = cronFieldMinute // range 0-59, value is -1
	m = m.cronStep(1)
	assert.Equal(t, 0, m.values[cronFieldMinute])
}

func TestCronStep_DownFromAny_GoesToMax(t *testing.T) {
	m := newCronPickerModel("")
	m.focused = cronFieldMinute
	m = m.cronStep(-1)
	assert.Equal(t, 59, m.values[cronFieldMinute])
}

func TestCronStep_UpPastMax_WrapsToAny(t *testing.T) {
	m := newCronPickerModel("")
	m.focused = cronFieldMinute
	m.values[cronFieldMinute] = 59
	m = m.cronStep(1)
	assert.Equal(t, -1, m.values[cronFieldMinute])
}

func TestCronStep_DownPastMin_WrapsToAny(t *testing.T) {
	m := newCronPickerModel("")
	m.focused = cronFieldMinute
	m.values[cronFieldMinute] = 0
	m = m.cronStep(-1)
	assert.Equal(t, -1, m.values[cronFieldMinute])
}

func TestCronStep_NormalIncrement(t *testing.T) {
	m := newCronPickerModel("")
	m.focused = cronFieldHour
	m.values[cronFieldHour] = 10
	m = m.cronStep(1)
	assert.Equal(t, 11, m.values[cronFieldHour])
}

func TestCronStep_MonthRange(t *testing.T) {
	// Month range is 1-12
	m := newCronPickerModel("")
	m.focused = cronFieldMonth
	m = m.cronStep(1) // -1 → 1
	assert.Equal(t, 1, m.values[cronFieldMonth])
	m.values[cronFieldMonth] = 12
	m = m.cronStep(1) // 12 → -1
	assert.Equal(t, -1, m.values[cronFieldMonth])
}

func TestCronStep_WeekdayRange(t *testing.T) {
	// Weekday range is 0-6
	m := newCronPickerModel("")
	m.focused = cronFieldDayOfWeek
	m = m.cronStep(-1) // -1 → 6
	assert.Equal(t, 6, m.values[cronFieldDayOfWeek])
	m = m.cronStep(1) // 6 → -1 (past max)
	// 6+1=7 > 6, so wraps to -1
	assert.Equal(t, -1, m.values[cronFieldDayOfWeek])
}

func TestCronStep_OnlyAffectsFocusedField(t *testing.T) {
	m := newCronPickerModel("0 20 * * 5")
	m.focused = cronFieldHour
	m = m.cronStep(1)
	// Only hour changes
	assert.Equal(t, 21, m.values[cronFieldHour])
	assert.Equal(t, 0, m.values[cronFieldMinute])
	assert.Equal(t, 5, m.values[cronFieldDayOfWeek])
}

// ── cronPickerModel.fieldDisplay ──────────────────────────────────────────────

func TestFieldDisplay_AnyValue(t *testing.T) {
	m := newCronPickerModel("")
	for i := 0; i < cronFieldCount; i++ {
		assert.Equal(t, "*", m.fieldDisplay(i))
	}
}

func TestFieldDisplay_MinuteZeroPadded(t *testing.T) {
	m := newCronPickerModel("")
	m.values[cronFieldMinute] = 5
	assert.Equal(t, "05", m.fieldDisplay(cronFieldMinute))
	m.values[cronFieldMinute] = 59
	assert.Equal(t, "59", m.fieldDisplay(cronFieldMinute))
}

func TestFieldDisplay_HourZeroPadded(t *testing.T) {
	m := newCronPickerModel("")
	m.values[cronFieldHour] = 0
	assert.Equal(t, "00", m.fieldDisplay(cronFieldHour))
	m.values[cronFieldHour] = 23
	assert.Equal(t, "23", m.fieldDisplay(cronFieldHour))
}

func TestFieldDisplay_DayNotPadded(t *testing.T) {
	m := newCronPickerModel("")
	m.values[cronFieldDayOfMonth] = 1
	assert.Equal(t, "1", m.fieldDisplay(cronFieldDayOfMonth))
	m.values[cronFieldDayOfMonth] = 31
	assert.Equal(t, "31", m.fieldDisplay(cronFieldDayOfMonth))
}

func TestFieldDisplay_MonthName(t *testing.T) {
	m := newCronPickerModel("")
	cases := map[int]string{1: "Jan", 6: "Jun", 12: "Dec"}
	for v, want := range cases {
		m.values[cronFieldMonth] = v
		assert.Equal(t, want, m.fieldDisplay(cronFieldMonth))
	}
}

func TestFieldDisplay_WeekdayName(t *testing.T) {
	m := newCronPickerModel("")
	cases := map[int]string{0: "Sun", 1: "Mon", 5: "Fri", 6: "Sat"}
	for v, want := range cases {
		m.values[cronFieldDayOfWeek] = v
		assert.Equal(t, want, m.fieldDisplay(cronFieldDayOfWeek))
	}
}

// ── cronPickerModel.toCronExpr ────────────────────────────────────────────────

func TestToCronExpr_AllWildcard(t *testing.T) {
	m := newCronPickerModel("")
	assert.Equal(t, "* * * * *", m.toCronExpr())
}

func TestToCronExpr_AllSpecific(t *testing.T) {
	m := newCronPickerModel("30 20 15 6 5")
	assert.Equal(t, "30 20 15 6 5", m.toCronExpr())
}

func TestToCronExpr_Mixed(t *testing.T) {
	m := newCronPickerModel("")
	m.values[cronFieldMinute] = 0
	m.values[cronFieldHour] = 20
	// day, month, weekday remain -1
	assert.Equal(t, "0 20 * * *", m.toCronExpr())
}

func TestToCronExpr_RoundTrip(t *testing.T) {
	// Parse an expression then convert back — should match
	exprs := []string{
		"0 0 * * *",
		"30 14 1 6 5",
		"0 20 * * 5",
	}
	for _, expr := range exprs {
		m := newCronPickerModel(expr)
		assert.Equal(t, expr, m.toCronExpr(), "round-trip failed for %q", expr)
	}
}

// ── cronPickerModel.nextStr ───────────────────────────────────────────────────

func TestNextStr_AllWildcard(t *testing.T) {
	m := newCronPickerModel("")
	assert.Equal(t, "every minute", m.nextStr())
}

func TestNextStr_ValidExpression_NonEmpty(t *testing.T) {
	m := newCronPickerModel("0 20 * * 5")
	s := m.nextStr()
	assert.NotEmpty(t, s)
	assert.NotEqual(t, "—", s)
}

func TestNextStr_ContainsDateComponents(t *testing.T) {
	// "0 0 * * *" always has a next occurrence; result should contain year
	m := newCronPickerModel("0 0 * * *")
	s := m.nextStr()
	assert.Contains(t, s, "2026")
}

// ── expiryDaysInMonth ─────────────────────────────────────────────────────────

func TestExpiryDaysInMonth_StandardMonths(t *testing.T) {
	cases := []struct {
		year, month, want int
	}{
		{2026, 1, 31},
		{2026, 3, 31},
		{2026, 4, 30},
		{2026, 6, 30},
		{2026, 9, 30},
		{2026, 11, 30},
		{2026, 12, 31},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d-%02d", c.year, c.month), func(t *testing.T) {
			assert.Equal(t, c.want, expiryDaysInMonth(c.year, c.month))
		})
	}
}

func TestExpiryDaysInMonth_FebruaryLeapYear(t *testing.T) {
	assert.Equal(t, 29, expiryDaysInMonth(2024, 2))
}

func TestExpiryDaysInMonth_FebruaryNonLeapYear(t *testing.T) {
	assert.Equal(t, 28, expiryDaysInMonth(2026, 2))
}

// ── newExpiryPickerModel ──────────────────────────────────────────────────────

func TestNewExpiryPickerModel_EmptyString_DefaultsToNow(t *testing.T) {
	before := time.Now()
	m := newExpiryPickerModel("")
	after := time.Now()

	assert.GreaterOrEqual(t, m.year, before.Year())
	assert.LessOrEqual(t, m.year, after.Year())
	assert.GreaterOrEqual(t, m.month, 1)
	assert.LessOrEqual(t, m.month, 12)
	assert.GreaterOrEqual(t, m.day, 1)
	assert.LessOrEqual(t, m.day, 31)
	assert.GreaterOrEqual(t, m.hour, 0)
	assert.LessOrEqual(t, m.hour, 23)
	assert.GreaterOrEqual(t, m.minute, 0)
	assert.LessOrEqual(t, m.minute, 59)
}

func TestNewExpiryPickerModel_ValidRFC3339(t *testing.T) {
	m := newExpiryPickerModel("2027-06-15T14:30:00Z")
	assert.Equal(t, 2027, m.year)
	assert.Equal(t, 6, m.month)
	assert.Equal(t, 15, m.day)
	assert.Equal(t, 14, m.hour)
	assert.Equal(t, 30, m.minute)
}

func TestNewExpiryPickerModel_InvalidRFC3339_FallsBackToNow(t *testing.T) {
	before := time.Now()
	m := newExpiryPickerModel("not-a-timestamp")
	after := time.Now()

	// Should still produce a valid model defaulting to now
	assert.GreaterOrEqual(t, m.year, before.Year())
	assert.LessOrEqual(t, m.year, after.Year())
}

func TestNewExpiryPickerModel_FocusedDefaultsToZero(t *testing.T) {
	m := newExpiryPickerModel("")
	assert.Equal(t, 0, m.focused)
}

// ── expiryPickerModel.step ────────────────────────────────────────────────────

func TestExpiryStep_YearIncrements(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T00:00:00Z")
	m.focused = expiryFieldYear
	m = m.step(1)
	assert.Equal(t, 2027, m.year)
	m = m.step(-3)
	assert.Equal(t, 2024, m.year)
}

func TestExpiryStep_MonthCyclicUp(t *testing.T) {
	m := newExpiryPickerModel("2026-12-15T00:00:00Z")
	m.focused = expiryFieldMonth
	m = m.step(1) // 12 → 1
	assert.Equal(t, 1, m.month)
}

func TestExpiryStep_MonthCyclicDown(t *testing.T) {
	m := newExpiryPickerModel("2026-01-15T00:00:00Z")
	m.focused = expiryFieldMonth
	m = m.step(-1) // 1 → 12
	assert.Equal(t, 12, m.month)
}

func TestExpiryStep_MonthClampsDayToMaxDays(t *testing.T) {
	// Start on Jan 31; step to February (28 days in 2026)
	m := newExpiryPickerModel("2026-01-31T00:00:00Z")
	m.focused = expiryFieldMonth
	m = m.step(1) // Jan → Feb
	assert.Equal(t, 2, m.month)
	assert.Equal(t, 28, m.day)
}

func TestExpiryStep_DayCyclicUp(t *testing.T) {
	m := newExpiryPickerModel("2026-04-30T00:00:00Z") // April has 30 days
	m.focused = expiryFieldDay
	m = m.step(1) // 30 → 1
	assert.Equal(t, 1, m.day)
}

func TestExpiryStep_DayCyclicDown(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T00:00:00Z")
	m.focused = expiryFieldDay
	m = m.step(-1) // 1 → 30
	assert.Equal(t, 30, m.day)
}

func TestExpiryStep_HourCyclicUp(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T23:00:00Z")
	m.focused = expiryFieldHour
	m = m.step(1) // 23 → 0
	assert.Equal(t, 0, m.hour)
}

func TestExpiryStep_HourCyclicDown(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T00:00:00Z")
	m.focused = expiryFieldHour
	m = m.step(-1) // 0 → 23
	assert.Equal(t, 23, m.hour)
}

func TestExpiryStep_MinuteCyclicUp(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T00:59:00Z")
	m.focused = expiryFieldMinute
	m = m.step(1) // 59 → 0
	assert.Equal(t, 0, m.minute)
}

func TestExpiryStep_MinuteCyclicDown(t *testing.T) {
	m := newExpiryPickerModel("2026-04-01T00:00:00Z")
	m.focused = expiryFieldMinute
	m = m.step(-1) // 0 → 59
	assert.Equal(t, 59, m.minute)
}

func TestExpiryStep_OnlyAffectsFocusedField(t *testing.T) {
	m := newExpiryPickerModel("2026-06-15T10:30:00Z")
	m.focused = expiryFieldHour
	m = m.step(1)
	assert.Equal(t, 11, m.hour)
	// All other fields unchanged
	assert.Equal(t, 2026, m.year)
	assert.Equal(t, 6, m.month)
	assert.Equal(t, 15, m.day)
	assert.Equal(t, 30, m.minute)
}
