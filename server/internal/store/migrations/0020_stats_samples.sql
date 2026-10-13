-- Host CPU/memory samples for the admin dashboard's charts.
--
-- Only the coarse series (one sample a minute) is persisted: the fine series
-- exists to make the last hour look smooth and is not worth a write every
-- few seconds. Before this the whole history lived in memory and a restart
-- wiped the charts, which made them useless for the one question an
-- operator actually has — was the server struggling while I was out?
--
-- `at` is the primary key, so a sample taken twice for the same minute
-- replaces rather than duplicates.

CREATE TABLE stats_samples (
    at        TEXT PRIMARY KEY,
    cpu_pct   REAL    NOT NULL,
    mem_used  INTEGER NOT NULL,
    mem_total INTEGER NOT NULL,
    proc_rss  INTEGER NOT NULL,
    num_cpu   INTEGER NOT NULL
);
