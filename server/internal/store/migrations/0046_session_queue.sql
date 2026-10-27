-- The play queue on a session grows from ids and an index into the whole of
-- what a screen is playing (server/docs/music.md, part 2): entries in play
-- order with their unshuffled place, shuffle and repeat, where in the
-- current entry, and what the queue was made from. Stored as JSON in
-- sessions.queue as before; this rewrites what is there into the new shape
-- (the store reads the old one too, for a client that still sends it).
UPDATE sessions SET queue = json_object(
    'entries', (SELECT json_group_array(json_object('id', value, 'ord', key)) FROM json_each(queue, '$.itemIds')),
    'index', COALESCE(json_extract(queue, '$.index'), 0),
    'repeat', 'off')
WHERE queue <> '' AND json_valid(queue) AND json_type(queue, '$.itemIds') = 'array';
