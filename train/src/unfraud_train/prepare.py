"""Normalizing raw payment datasets into the Payment Schema.

Every payment Profile reads one file, <raw>/<profile>/events.csv, sorted by
Event Time. It starts with the core columns, which mean the same thing in
every dataset, followed by the dataset's own named extras:

    event_id, event_time_ms, payer, payee, customer, amount_usd, channel,
    category, is_fraud, <extras...>

- payer: the paying instrument (a card, or a source bank account)
- payee: who is paid (a merchant, or a destination bank account)
- customer: who owns the payer (a cardholder, or the account's legal entity)
- channel: in_person or online for cards; the payment format for transfers
- category: what was bought, in Sparkov's vocabulary (TabFormer's MCCs are
  mapped onto it); empty for bank transfers

The output is deterministic, so its checksum is recorded with the raw files'.
"""

from __future__ import annotations

from pathlib import Path

import polars as pl

CORE = ["event_id", "event_time_ms", "payer", "payee", "customer", "amount_usd", "channel", "category", "is_fraud"]

# Merchant Category Codes in TabFormer, mapped onto Sparkov's categories.
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

# AMLworld pays in 15 currencies at fixed simulator rates (units per US
# dollar): the medians of its transfers between US dollars and each
# currency, which agree in both directions. Bitcoin's rate drifts (±13%).
USD_RATE = {
    "US Dollar": 1.0, "Euro": 0.8534, "UK Pound": 0.7742, "Swiss Franc": 0.915, "Yuan": 6.6976, "Yen": 105.4,
    "Rupee": 73.444, "Ruble": 77.804, "Canadian Dollar": 1.3193, "Australian Dollar": 1.4128,
    "Mexican Peso": 21.1431, "Brazil Real": 5.6465, "Shekel": 3.377, "Saudi Riyal": 3.7511, "Bitcoin": 0.0000842,
}


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


def sparkov(raw: Path) -> pl.LazyFrame:
    """Sparkov (Kaggle "Credit Card Transactions Fraud Detection"): 1,000
    simulated cardholders, January 2019 to December 2020.

    Event Time comes from unix_time, which the simulator counts from 2012:
    its date strings repeat 2019-02-28 for 2012's leap day, so they are not
    ordered. Shifting unix_time to match the strings from March 2019 on
    places January and February 2019 one day early.
    """
    frames = [pl.scan_csv(raw / f, schema_overrides={"cc_num": pl.Utf8, "zip": pl.Utf8})
              for f in ("fraudTrain.csv", "fraudTest.csv")]
    shift_s = 220_838_400
    cat = pl.col("category")
    t = pl.from_epoch(pl.col("unix_time") + shift_s, time_unit="s")
    lat1, lon1, lat2, lon2 = (pl.col(c).radians() for c in ("lat", "long", "merch_lat", "merch_long"))
    hav = ((lat2 - lat1) / 2).sin() ** 2 + lat1.cos() * lat2.cos() * ((lon2 - lon1) / 2).sin() ** 2
    return (
        pl.concat(frames)
        .with_row_index("row")
        .sort(["unix_time", "row"])
        .select(
            pl.col("trans_num").alias("event_id"),
            ((pl.col("unix_time") + shift_s) * 1000).alias("event_time_ms"),
            pl.col("cc_num").alias("payer"),
            pl.col("merchant").str.strip_prefix("fraud_").alias("payee"),
            pl.col("cc_num").alias("customer"),
            pl.col("amt").cast(pl.Float64).alias("amount_usd"),
            pl.when(cat.str.ends_with("_net")).then(pl.lit("online"))
            .when(cat.str.ends_with("_pos")).then(pl.lit("in_person")).alias("channel"),
            cat.str.replace(r"_(net|pos)$", "").alias("category"),
            pl.col("is_fraud").cast(pl.Int8),
            # Extras.
            pl.col("gender"),
            pl.col("state").alias("home_state"),
            pl.col("city_pop").cast(pl.Float64),
            pl.col("job"),
            (t.dt.year() - pl.col("dob").str.slice(0, 4).cast(pl.Int32)).cast(pl.Float64).alias("customer_age"),
            (2 * 6371.0 * hav.sqrt().arcsin()).alias("merchant_km"),
        )
    )


def amlworld(raw: Path) -> pl.LazyFrame:
    """IBM AMLworld HI-Small: bank transfers in a simulated economy, with
    laundering labelled. Transfers from 2022-09-11 on are dropped: after ten
    days of normal traffic the simulator only finishes laundering attempts
    (1,108 transfers, 59% laundering)."""
    names = ["ts", "from_bank", "from_account", "to_bank", "to_account", "amount_received", "receiving_currency",
             "amount_paid", "payment_currency", "payment_format", "is_laundering"]
    tx = pl.scan_csv(raw / "HI-Small_Trans.csv", has_header=False, skip_rows=1, new_columns=names,
                     schema_overrides={n: pl.Utf8 for n in names})
    owners = (pl.scan_csv(raw / "HI-Small_accounts.csv", infer_schema=False)
              .select(pl.col("Account Number").alias("account"), pl.col("Entity ID").alias("entity"))
              .unique("account", keep="first"))
    t = pl.col("ts").str.to_datetime("%Y/%m/%d %H:%M")
    payer = pl.format("{}-{}", "from_bank", "from_account")
    payee = pl.format("{}-{}", "to_bank", "to_account")
    paid = pl.col("amount_paid").cast(pl.Float64)
    return (
        tx.with_row_index("row")
        .with_columns(t=t)
        .filter(pl.col("t") < pl.datetime(2022, 9, 11))
        .join(owners.rename({"account": "from_account", "entity": "payer_entity"}), on="from_account", how="left")
        .join(owners.rename({"account": "to_account", "entity": "payee_entity"}), on="to_account", how="left")
        .sort(["t", "row"])
        .select(
            pl.col("row").cast(pl.Utf8).alias("event_id"),
            pl.col("t").dt.epoch("ms").alias("event_time_ms"),
            payer.alias("payer"),
            payee.alias("payee"),
            pl.col("payer_entity").alias("customer"),
            (paid / pl.col("payment_currency").replace_strict(USD_RATE, return_dtype=pl.Float64)).alias("amount_usd"),
            pl.col("payment_format").str.to_lowercase().str.replace(" ", "_").alias("channel"),
            pl.lit(None, dtype=pl.Utf8).alias("category"),
            pl.col("is_laundering").cast(pl.Int8).alias("is_fraud"),
            # Extras.
            pl.col("payment_currency"),
            pl.col("receiving_currency"),
            (pl.col("payment_currency") != pl.col("receiving_currency")).cast(pl.Float64).alias("cross_currency"),
            (payer == payee).cast(pl.Float64).alias("self_transfer"),
            (pl.col("from_bank") == pl.col("to_bank")).cast(pl.Float64).alias("same_bank"),
            (pl.col("payer_entity") == pl.col("payee_entity")).cast(pl.Float64).alias("same_owner"),
        )
    )


DATASETS = {"tabformer": tabformer, "sparkov": sparkov, "amlworld": amlworld}


def prepare(raw_root: Path, profile: str) -> Path:
    lf = DATASETS[profile](raw_root / profile)
    assert lf.collect_schema().names()[: len(CORE)] == CORE
    out = raw_root / profile / "events.csv"
    tmp = out.with_suffix(".csv.tmp")
    lf.sink_csv(tmp, float_precision=None, engine="streaming", maintain_order=True)
    tmp.rename(out)
    return out
