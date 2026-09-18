# Tariff snapshots

The bot loads the newest CSV filename (lexically) that contains an exact match
for `LLM_PROVIDER` and `OPENAI_MODEL`. Keep filenames in this form so their
date order is unambiguous:

```text
YYYY-MM-DD-provider.csv
```

To update rates, copy the newest file, rename it with the date of verification,
and update the rows from the official provider price page. Keep all models that
the bot may use in every monthly snapshot. The filename is saved in each
`llm_requests.pricing_version` record.

Rates are whole micro-USD per one million tokens. For example, `$0.15 / 1M`
is `150000`. This loader handles only normal text-token pricing; model-specific
tool, image, audio, long-context, or regional-processing charges need separate
support before using those features.

CSV format:

```csv
provider,model,input_micro_usd_per_million,cached_input_micro_usd_per_million,output_micro_usd_per_million,source_url
openai,gpt-4o-mini,150000,75000,600000,https://developers.openai.com/api/docs/models/gpt-4o-mini
```
