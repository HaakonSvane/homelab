-- Day of the month a bill is drawn (1–31, 31 = last day), per version so a new
-- agreement can move it. NULL = not set, which the app counts as the 1st (the
-- cautious assumption for the felleskonto buffer).
ALTER TABLE expense_versions ADD COLUMN due_day INTEGER CHECK (due_day IS NULL OR due_day BETWEEN 1 AND 31);
