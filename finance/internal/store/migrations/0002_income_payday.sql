-- Payday (day of month the net income arrives) on each income version. NULL means
-- not set. 31 means the last day of the month; pay dates falling on a weekend or a
-- bank holiday are moved to the previous working day by the app, not stored here.
ALTER TABLE incomes ADD COLUMN payday INTEGER CHECK (payday IS NULL OR payday BETWEEN 1 AND 31);
