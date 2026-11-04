-- shape is what a project page's revision judged the project to be: a
-- system its repositories make together ('system'), or a holder of
-- repositories that stand on their own ('collection'), whose page is a short
-- summary with no statements and no map. Project pages only; NULL on every
-- revision written before, read as a system until a project step judges.
-- Every new project revision carries the current one forward unless its
-- writer judged anew.
ALTER TABLE revisions ADD COLUMN shape TEXT
    CHECK (shape IS NULL OR (scope = 'project' AND shape IN ('system', 'collection')));
