CREATE TABLE listings (
    id          text          PRIMARY KEY,
    sku         varchar(64)   NOT NULL,

    -- Widened. Every existing value still fits.
    title       varchar(500)  NOT NULL,

    grade       smallint      NOT NULL,
    price_cents bigint        NOT NULL,
    currency    char(3)       NOT NULL DEFAULT 'EUR',
    seller_id   text          NOT NULL,
    in_stock    boolean       NOT NULL DEFAULT true,
    notes       text,
    created_at  timestamptz   NOT NULL DEFAULT now(),

    -- Added with a default, so the old version's inserts still work.
    battery_pct smallint      NOT NULL DEFAULT 100,

    -- Added and nullable, which is also safe.
    warehouse   text
);

CREATE TABLE watches (
    person_id  text        NOT NULL,
    listing_id text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, listing_id)
);

-- A new table nothing running refers to yet.
CREATE TABLE price_history (
    listing_id  text        NOT NULL,
    price_cents bigint      NOT NULL,
    seen_at     timestamptz NOT NULL DEFAULT now()
);
