-- Audio streams discovered during scans. A file often carries several: other
-- languages, a commentary, or a stereo mix beside the surround one. The
-- scanner catalogues them so the player can offer a picker, and so a chosen
-- track can be named without reprobing the file.

CREATE TABLE audio_tracks (
    id           TEXT PRIMARY KEY,
    item_id      TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    stream_index INTEGER NOT NULL,
    codec        TEXT NOT NULL DEFAULT '',
    language     TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    channels     INTEGER NOT NULL DEFAULT 0,
    bitrate      INTEGER NOT NULL DEFAULT 0,
    is_default   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_audio_tracks_item ON audio_tracks(item_id);
