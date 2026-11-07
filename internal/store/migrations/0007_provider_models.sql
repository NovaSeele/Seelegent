-- The model allow-list of a provider as a list, exactly as last set (a JSON array of model ids). allowed_models
-- stays the pattern that is enforced; when models is not empty, allowed_models is built from it. NULL marks a row
-- written before this column existed: internal/account derives the list from allowed_models when that pattern was
-- built from a list, and answers an empty list for a hand-written pattern.
ALTER TABLE providers ADD COLUMN models TEXT;
