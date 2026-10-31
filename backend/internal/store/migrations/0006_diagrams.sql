-- The project's system map: rongo's flow form (ui/src/diagramIR.ts) as the
-- baseline project step returned it and baseline/diagram.go validated it.
-- Project pages only; a statement's diagram lives inside its statement in
-- the statements column and needs no column of its own. NULL until a
-- baseline project step writes one; the merge path carries it forward.
ALTER TABLE revisions ADD COLUMN map JSONB,
    ADD CONSTRAINT revisions_map_scope CHECK (map IS NULL OR scope = 'project');
