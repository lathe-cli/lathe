package testutil

func Require(tb TB, ok bool, format string, args ...any) {
	tb.Helper()
	if !ok {
		tb.Fatalf(format, args...)
	}
}

func Check(tb TB, ok bool, format string, args ...any) {
	tb.Helper()
	if !ok {
		tb.Errorf(format, args...)
	}
}

func NoError(tb TB, err error) {
	tb.Helper()
	Require(tb, err == nil, "unexpected error: %v", err)
}
