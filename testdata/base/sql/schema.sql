-- The listing table the catalogue service reads.
CREATE TABLE listings (
    id          text          PRIMARY KEY,
    sku         varchar(64)   NOT NULL,
    title       varchar(200)  NOT NULL,
    grade       smallint      NOT NULL,
    price_cents bigint        NOT NULL,
    currency    char(3)       NOT NULL DEFAULT 'EUR',
    seller_id   text          NOT NULL,
    in_stock    boolean       NOT NULL DEFAULT true,
    notes       text,
    created_at  timestamptz   NOT NULL DEFAULT now()
);

CREATE TABLE watches (
    person_id  text        NOT NULL,
    listing_id text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, listing_id)
);
