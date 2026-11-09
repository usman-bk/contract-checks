CREATE TABLE listings (
    id          text          PRIMARY KEY,

    -- Narrowed from 64.
    sku         varchar(32)   NOT NULL,

    -- Widened, which is safe.
    title       text          NOT NULL,

    -- Narrowed from smallint is not possible, so this is a widening.
    grade       integer       NOT NULL,

    -- Narrowed from bigint. Existing values may not fit.
    price_cents integer       NOT NULL,

    -- The default is gone, so an insert that relied on it now fails.
    currency    char(3)       NOT NULL,

    seller_id   text          NOT NULL,
    in_stock    boolean       NOT NULL DEFAULT true,

    -- Was nullable, now required, with no default.
    notes       text          NOT NULL,

    created_at  timestamptz   NOT NULL DEFAULT now(),

    -- Added, NOT NULL, no default. The old version is still inserting rows
    -- and knows nothing about it.
    warehouse   text          NOT NULL
);

-- The watches table is gone entirely.
