"""Preparing TabFormer for the engine.

The raw files are sorted by card, not time, and spread over three files.
prepare writes <raw>/tabformer/events.csv: one row per transaction in Event
Time order, with the card (payer), merchant (payee), cardholder (customer),
amount, channel, a merchant category, the label, then the card's and
cardholder's attributes. The output is deterministic, so its checksum is
recorded with the raw files'.
"""

from __future__ import annotations

from pathlib import Path

import polars as pl

# Merchant Category Codes grouped into 11 categories.
# Ranges first: 3000-3299 airlines, 3300-3499 car rental, 3500-3999 hotels.
MCC_CATEGORY = {
    1711: "home", 4111: "gas_transport", 4112: "travel", 4121: "gas_transport", 4131: "gas_transport",
    4214: "misc", 4411: "travel", 4511: "travel", 4722: "travel", 4784: "gas_transport", 4814: "home",
    4829: "misc", 4899: "entertainment", 4900: "home", 5045: "shopping", 5094: "shopping", 5192: "shopping",
    5193: "shopping", 5211: "home", 5251: "home", 5261: "home", 5300: "grocery", 5310: "shopping",
    5311: "shopping", 5411: "grocery", 5499: "grocery", 5533: "misc", 5541: "gas_transport", 5621: "shopping",
    5651: "shopping", 5655: "shopping", 5661: "shopping", 5712: "home", 5719: "home", 5722: "home",
    5732: "shopping", 5733: "entertainment", 5812: "food_dining", 5813: "food_dining", 5814: "food_dining",
    5815: "entertainment", 5816: "entertainment", 5912: "health_fitness", 5921: "grocery", 5932: "shopping",
    5941: "shopping", 5942: "shopping", 5947: "shopping", 5970: "shopping", 5977: "personal_care",
    6300: "misc", 7011: "travel", 7210: "personal_care", 7230: "personal_care", 7276: "misc", 7349: "home",
    7393: "misc", 7531: "misc", 7538: "misc", 7542: "misc", 7549: "misc", 7801: "entertainment",
    7802: "entertainment", 7832: "entertainment", 7922: "entertainment", 7995: "entertainment",
    7996: "entertainment", 8011: "health_fitness", 8021: "health_fitness", 8041: "health_fitness",
    8043: "health_fitness", 8049: "health_fitness", 8062: "health_fitness", 8099: "health_fitness",
    8111: "misc", 8931: "misc", 9402: "misc",
}

US_STATES = set(
    "AL AK AZ AR CA CO CT DE DC FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO MT NE NV NH NJ NM NY NC ND "
    "OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY".split()
)

def _money(col: str) -> pl.Expr:
    return pl.col(col).str.replace(r"^\$", "").cast(pl.Float64)


def _mcc_category() -> pl.Expr:
    mcc = pl.col("MCC")
    return (
        pl.when(mcc.is_between(3000, 3999)).then(pl.lit("travel"))
        .otherwise(mcc.replace_strict(MCC_CATEGORY, default=None, return_dtype=pl.Utf8))
    )


def tabformer(raw: Path) -> pl.LazyFrame:
    """IBM TabFormer: 2,000 synthetic US cardholders over three decades.

    Only October 2014 to December 2019 is kept: earlier years have few
    cardholders, 2020 has no fraud labels yet, and three months of warm-up
    precede the first training month so velocity features have history.
    """
    tx = pl.scan_csv(raw / "credit_card_transactions-ibm_v2.csv",
                     schema_overrides={"Merchant Name": pl.Utf8, "Zip": pl.Float64, "Errors?": pl.Utf8})
    users = pl.scan_csv(raw / "sd254_users.csv").with_row_index("User").select(
        pl.col("User").cast(pl.Int64),
        pl.col("State").alias("home_state"),
        pl.col("Birth Year").alias("birth_year"),
        _money("Yearly Income - Person").alias("yearly_income"),
        pl.col("FICO Score").cast(pl.Float64).alias("fico_score"),
    )
    cards = pl.scan_csv(raw / "sd254_cards.csv").select(
        pl.col("User").cast(pl.Int64),
        pl.col("CARD INDEX").cast(pl.Int64).alias("Card"),
        pl.col("Card Brand").str.to_lowercase().alias("card_brand"),
        pl.col("Card Type").str.to_lowercase().alias("card_type"),
        (pl.col("Has Chip") == "YES").cast(pl.Float64).alias("card_has_chip"),
        _money("Credit Limit").alias("credit_limit"),
    )
    t = pl.datetime(pl.col("Year"), pl.col("Month"), pl.col("Day"),
                    pl.col("Time").str.slice(0, 2).cast(pl.Int32), pl.col("Time").str.slice(3, 2).cast(pl.Int32))
    use = pl.col("Use Chip")
    online = use == "Online Transaction"
    state = pl.col("Merchant State")
    return (
        tx.with_row_index("row")
        .with_columns(t=t)
        .filter(pl.col("t") >= pl.datetime(2014, 10, 1), pl.col("t") < pl.datetime(2020, 1, 1))
        .join(cards, on=["User", "Card"], how="left")
        .join(users, on="User", how="left")
        .sort(["t", "row"])
        .select(
            pl.col("row").cast(pl.Utf8).alias("event_id"),
            pl.col("t").dt.epoch("ms").alias("event_time_ms"),
            pl.format("{}-{}", "User", "Card").alias("payer"),
            pl.col("Merchant Name").alias("payee"),
            pl.col("User").cast(pl.Utf8).alias("customer"),
            _money("Amount").alias("amount_usd"),
            pl.when(online).then(pl.lit("online")).otherwise(pl.lit("in_person")).alias("channel"),
            _mcc_category().alias("category"),
            (pl.col("Is Fraud?") == "Yes").cast(pl.Int8).alias("is_fraud"),
            # Extras.
            use.str.replace(" Transaction", "").str.to_lowercase().alias("entry_mode"),
            pl.col("MCC").cast(pl.Utf8).alias("mcc"),
            pl.col("Errors?").alias("errors"),
            state.alias("merchant_state"),
            # A merchant state that is not a US state is a country.
            pl.when(online | state.is_null()).then(None)
            .otherwise((~state.is_in(list(US_STATES))).cast(pl.Float64)).alias("abroad"),
            pl.when(online | state.is_null()).then(None)
            .otherwise((state != pl.col("home_state")).cast(pl.Float64)).alias("out_of_home_state"),
            "card_brand", "card_type", "card_has_chip", "credit_limit",
            (pl.col("Year") - pl.col("birth_year")).cast(pl.Float64).alias("customer_age"),
            "yearly_income", "fico_score",
        )
    )


def prepare(raw_root: Path) -> Path:
    lf = tabformer(raw_root / "tabformer")
    out = raw_root / "tabformer" / "events.csv"
    tmp = out.with_suffix(".csv.tmp")
    lf.sink_csv(tmp, float_precision=None, engine="streaming", maintain_order=True)
    tmp.rename(out)
    return out
