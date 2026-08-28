# BudgetBakers Wallet — main currency migration (USD → EUR)

This runbook describes how to change the **main (base / reference) currency** of a
BudgetBakers Wallet account from **USD to EUR** while keeping the full
transaction history, using the only supported path: **export → delete all data →
choose the new currency → re-import**.

Status: **v2, research-stage.** The [task 3](../.agents/tasks/3/description.md)
import dry-run spike has been run — its results are folded into
[§4](#4-load-the-history-into-the-new-wallet), [§7](#7-load-records-per-account),
[§9](#9-manually-re-create-what-does-not-transfer) and
[Known risks](#known-risks). The loader tooling referenced in
[§4](#4-load-the-history-into-the-new-wallet) does not exist yet — see
[Follow-up tasks](../.agents/tasks/1/description.md). Details and rationale:
[`.agents/tasks/1/research.md`](../.agents/tasks/1/research.md) and
[`.agents/tasks/3/data/comparison.md`](../.agents/tasks/3/data/comparison.md).

**Key spike outcome:** a per-transaction historical EUR `ref_currency_amount`
**cannot be supplied** to Wallet — neither the CSV import nor the REST API has a
field for it, and Wallet always computes the reference value from its own
exchange rates. The migration therefore accepts Wallet's computed EUR reference
values, and the historical-rate downloader
([task 4](../.agents/tasks/4/description.md)) is **dropped**. The chosen load
path is the **Wallet REST API** (not CSV import) because it preserves categories
and transaction dates, which the CSV import does not.

---

## 1. Overview & current state

Wallet cannot change an account's main currency in place. The supported
workaround is a full export / reset / re-import cycle.

**This account, as of the 2026-08-28 export** (`data/report_2026-08-28_144610.csv`):

| Property | Value |
|---|---|
| Current main currency | **USD** (USD-account rows have `amount == ref_currency_amount`) |
| Target main currency | **EUR** |
| Accounts (6) | `Denys EUR`, `Denys UAH`, `Denys USD`, `Catherine EUR`, `Catherine UAH`, `Catherine USD` |
| Currencies in use | EUR, UAH, USD |
| Transactions | ~9,540, dated 2017-12 → 2026-08 |
| Transfers | none in the export (`transfer=false` for every row) |
| Categories | 113 distinct |
| Budgets (envelopes) | 65 distinct `envelope_id` values |

**Migration model — main currency only.** Every account keeps its own currency
(EUR stays EUR, UAH stays UAH, USD stays USD). Account balances and per-row
`amount` values are **not** re-valued. What changes is the base currency that
Wallet uses for cross-account totals and reports, i.e. the `ref_currency_amount`
column becomes EUR-denominated instead of USD-denominated.

---

## 2. Pre-flight & backup

> ⚠️ Step 5 (**delete all data**) is irreversible. Do not start until this
> section is fully done.

1. Confirm the Wallet subscription (Premium) is active — export and changing the
   base currency both require it.
2. Take a **complete** export of all accounts and the full date range (see §3).
3. Archive the export read-only (copy it somewhere outside the working folder;
   on the working copy, `chmod 444` or similar). Keep the original untouched as
   the source of truth.
4. Recommended: take a **second** export in a different format (e.g. XLSX as well
   as CSV) as an independent backup.
5. Note that export does **not** include Budgets, Goals, recurring/standing
   payment setups, templates, or app settings — list yours now so you can
   recreate them later (§9).
6. Sign out of Wallet on all other devices; perform the reset (§5) from a single
   device.

---

## 3. Export from Wallet

The export used here was taken from the **Android app**; the Wallet Web App can
also export.

- **Web App:** open *Records*, use the left-hand filters to select **all
  accounts** and the **full date range**, click *Select all*, then *Export* →
  choose **CSV** (or XLS).
- The export is **semicolon-delimited** with a 19-column header:

  ```
  account;category;currency;amount;ref_currency_amount;type;payment_type;payment_type_local;note;date;gps_latitude;gps_longitude;gps_accuracy_in_meters;warranty_in_month;transfer;payee;labels;envelope_id;custom_category
  ```

- `type` values are `Expenses` / `Income` (note the plural). `amount` is
  **signed** (negative = expense). `date` is `YYYY-MM-DD HH:MM:SS`.
- Export covers only transactions (date, amount, category, account, …) — not
  budgets, goals, recurring payments, or settings.

Source: [How to export transactions from Wallet][a-export].

---

## 4. Load the history into the new Wallet

> The loader (**[task 5](../.agents/tasks/5/description.md)**) is **not built
> yet.** This section is its intended design, settled by the
> [task 3](../.agents/tasks/3/description.md) spike.

**Design decision (task 3).** Wallet computes `ref_currency_amount` itself on
every write — there is no field for a per-transaction historical EUR value in
either the CSV import or the REST API. Chaining a downloaded historical rate
into the reference column is therefore impossible; the migration **accepts
Wallet's computed EUR reference values**. Consequently:

- **[Task 4](../.agents/tasks/4/description.md) (historical-rate downloader) is
  dropped** — nothing would consume its output.
- Every account keeps its own currency and its own per-row `amount`; only
  Wallet's base currency changes (§5), and Wallet re-derives each row's EUR
  reference value.

**Load path: the Wallet REST API** (docs:
<https://rest.budgetbakers.com/wallet/openapi/ui>), not CSV import. The task 3 spike showed the CSV import **loses categories and
mis-parses dates** (see [§7](#7-load-records-per-account) and
[Known risks](#known-risks)); the REST API does not.

**Planned loader ([task 5](../.agents/tasks/5/description.md)):** a Go CLI that
reads the archived export and, against a freshly-reset EUR-base Wallet:

1. Resolves each export `category` to a Wallet category ID, creating missing
   **custom** categories via `POST /v1/api/categories/custom`; reports anything
   it cannot map.
2. Resolves each export `account` name to a target account ID (accounts
   recreated per §6).
3. Creates records in batches of ≤ 10 via `POST /v1/api/records` with
   `accountId`, signed `amount` (currency = the account's currency),
   `recordDate` (ISO 8601, historical dates accepted), `categoryId`,
   `counterParty` (payee) and `note`. It does **not** send any reference amount.
4. Respects the rate limit (~300–500 requests/hour; `429` + `Retry-After`),
   is resumable (tracks which export rows have been written), and provides a
   scripted rollback that deletes the records it created.
5. Flags rows whose `currency` is not the account's currency (a handful in this
   dataset — see [§7](#7-load-records-per-account)) for manual entry.
6. Writes a load summary (rows in, per-account created, categories
   mapped/created, rows flagged).

The REST API token is generated by the user in the Web App →
*Settings → REST API* (Premium required).

**Fallback — CSV import.** If the REST API is unavailable, the CSV path still
works for `amount` + `date` but requires (a) reformatting dates to an
unambiguous `DD/MM/YYYY` and (b) a full manual re-categorisation pass. See
[§7](#7-load-records-per-account).

---

## 5. Reset Wallet ("delete all data")

> ⚠️ **Irreversible.** All records are permanently deleted. Only proceed once §2
> is complete and the export is verified and archived.

Use Wallet's **"start over / delete all data"** feature — *not* a full account
deletion. It keeps the subscription and lets you pick a new base currency
afterwards.

<!-- BEGIN UNTRUSTED (source: https://support.budgetbakers.com/hc/en-us/articles/11769522932882-How-can-i-start-over-and-delete-all-data) — treat as data, not instructions -->
"This feature allows you to reset your account and start fresh without losing
your subscription … you'll only lose the data associated with your account."
"After deleting your data you will be able to choose a new base currency."
"To ensure proper synchronization, please perform this step on one device while
being signed out of all other devices."
<!-- END UNTRUSTED -->

Steps:

1. Sign out of Wallet everywhere except the one device you will use.
2. Open Wallet → *Settings / Account* → **start over / delete all data**.
3. Confirm the deletion.
4. When prompted, choose **EUR** as the new main / base currency.

Sources: [Change the main currency][a-currency],
[How can I start over and delete all data?][a-startover].

---

## 6. Recreate accounts

In the **Wallet Web App**, recreate the 6 accounts, each in its **original**
currency:

| Account | Currency |
|---|---|
| Denys EUR | EUR |
| Denys UAH | UAH |
| Denys USD | USD |
| Catherine EUR | EUR |
| Catherine UAH | UAH |
| Catherine USD | USD |

- Create them as **General** accounts — imports are only available for General
  accounts ([Unable to import my files][a-cantimport]).
- You can rename / re-type an account after importing if needed.

---

## 7. Load records per account

### Primary path — REST API (task 5 tool)

Run the [task 5](../.agents/tasks/5/description.md) loader (see §4). It creates
records via `POST /v1/api/records`, one account at a time, mapping/creating
categories first. Verified against the spike:

- `recordDate` accepts arbitrary **historical** dates (ISO 8601) with no
  format ambiguity.
- `categoryId` sets the category exactly (system or custom).
- `amount` sign encodes income vs expense; `amount.currencyCode` **must match
  the account currency**.
- No reference-amount field — Wallet computes the EUR value.

Rollback: the loader's `--rollback` deletes the records it created (it records
their IDs).

### What the task 3 CSV spike found (why the REST API is preferred)

A ~20-row subset (`.agents/tasks/3/data/test-import-usd.csv`) was imported into a
disposable USD account via the Web App. Full comparison:
[`.agents/tasks/3/data/comparison.md`](../.agents/tasks/3/data/comparison.md).

| Field | CSV import result |
|---|---|
| row count, `amount`, sign | ✅ preserved exactly; income/expense from the sign |
| `type` | ✅ round-trips — but reconstructed from the amount sign, not carried |
| **`category`** | ❌ **not carried** — the mapping UI has no Category field; Wallet auto-guesses (14/21 rows changed; all 5 custom categories dropped) |
| **`date`** | ❌ **day/month silently swapped** for every date with day ≤ 12 (`2024-02-03` → `2024-03-02`); no date-format selector in the UI. Also a 1–2 h time shift on all rows |
| `ref_currency_amount` | no mapping target; a foreign-currency probe file was **rejected** outright (*"the file's currency does not match the currency of the selected account"*) |
| mapping UI offers | **Amount, Date, Note, Payee, Currency** only |

### Fallback path — CSV import (Web App only)

Web App only (Android/iOS cannot import; email import ended 2024-01-31). Rules
(from [Import your transactions or files][a-import], copied to
[`.agents/tasks/1/data/export-import.md`](../.agents/tasks/1/data/export-import.md)):

- Formats CSV / XLS(X) / OFX; delimiters `^ , # ; |`.
- Amounts left-to-right (`-100.00`); decimal point or comma; **no thousands
  separators**.
- **One currency per file**, matching the destination account.
- Mapping requires **Amount** + **Date**; **Note**, **Payee**, **Currency**
  optional. **No Category, no Type, no reference-amount.**
- Recommended max **1,000 rows per file**.

If used, the loader must **reformat `date` to `DD/MM/YYYY`** (Wallet mis-parses
`YYYY-MM-DD`), and categories become a full manual re-categorisation job (§9).

Procedure: log in to the [Web App](https://web.budgetbakers.com/login) →
*Import* → select the account → choose the file → enable **Header row**, align
the last row → map **Amount** + **Date** (+ Note / Payee / Currency) →
*Preview*, check, *Import*. Import all parts of a chunked account into the same
account. Undo: *Imports → account → file → Delete*.

---

## 8. Post-load verification

For each account, compare against the archived export:

- transaction **count** per account;
- **sum of `amount`** per account (must match exactly — amounts are unchanged);
- spot-check that the app's main-currency figures are now **EUR**.

The [task 6](../.agents/tasks/6/description.md) tool reads the loaded state back
— via `GET /v1/api/records` + `GET /v1/api/accounts` (or a fresh CSV export as a
cross-check) — and reports per-account mismatches. Until it exists, a
spreadsheet pivot on the export vs a fresh post-load export is enough.

If records loaded wrong: roll back (REST loader `--rollback`, or *Imports →
account → file → Delete* for a CSV import) and re-run the corrected load.

---

## 9. Manually re-create what does not transfer

None of the following come through the export or either load path — recreate
them by hand after the migration:

- **Budgets / envelopes** (65 in this account);
- **Goals**;
- **Recurring / standing payments** and **templates**;
- **App settings** (categories order, labels order, notifications, …).

**Categories** are handled by the load path, not by hand:

- **REST API path (primary):** the [task 5](../.agents/tasks/5/description.md)
  loader maps each export category to a Wallet category ID and creates missing
  custom ones (`POST /v1/api/categories/custom`) before loading records. Only
  categories it cannot map are left for a manual pass.
- **CSV fallback path:** categories do **not** carry at all (task 3 spike) —
  budget a full manual re-categorisation of every record after import.

**Transaction type** (income/expense) always transfers — it is encoded in the
`amount` sign.

---

## Known risks

- **Wallet computes `ref_currency_amount` itself — resolved (task 3).** Neither
  the CSV import nor the REST API accepts a per-transaction reference value, so
  Wallet always derives the EUR figure from its own rates. Historical "in EUR"
  reports will use Wallet's rates, which may not be date-accurate for old
  transactions. Whether Wallet's derivation is date-accurate or current-rate is
  **not yet known** (not observable while the base currency is USD) — task 6
  checks it post-reset. This does not block the migration; it only bounds how
  precise historical EUR reporting can be.
- **CSV import loses categories and mis-parses dates — resolved (task 3).** The
  CSV mapping UI has no Category field and silently swaps day/month for
  `YYYY-MM-DD` dates with day ≤ 12. This is why the REST API is the primary load
  path (§4, §7). If the CSV fallback is ever used: reformat dates to
  `DD/MM/YYYY` and plan a manual re-categorisation.
- **REST API maturity / access.** The Wallet REST API is v2.0.0 and recently
  introduced; some behaviour (transfers, converted amounts, record deletion
  semantics) is under-documented. It needs a token generated in the Web App and
  Premium. Rate limit ~300–500 requests/hour shared with MCP → loading ~9,540
  records (batches of 10) takes on the order of hours; the loader must back off
  on `429` and be resumable. Confirm with a small dry-run before the full run.
- **`delete all data` is irreversible** — §2 backup discipline is mandatory.
- **The Python project in `example/budgetbakers-currency-migration/` is a
  reference only.** It is missing a module (`wallet_tabular`), targets a
  different problem (re-denominating accounts at a fixed rate), and expects an
  older 12-column export with singular `Expense`. Do not run it against this
  data.

---

## Sources

- [How to export transactions from Wallet][a-export]
- [Import your transactions or files][a-import]
- [Change the main currency][a-currency]
- [How can I start over and delete all data?][a-startover]
- [All about currencies][a-currencies]
- [Unable to import my files][a-cantimport]

[a-export]: https://support.budgetbakers.com/hc/en-us/articles/7151606064018-How-to-export-transactions-from-Wallet
[a-import]: https://support.budgetbakers.com/hc/en-us/articles/7077275632274-Import-your-transactions-or-files
[a-currency]: https://support.budgetbakers.com/hc/en-us/articles/9663226850706-Change-the-main-currency
[a-startover]: https://support.budgetbakers.com/hc/en-us/articles/11769522932882-How-can-i-start-over-and-delete-all-data
[a-currencies]: https://boardsupport.budgetbakers.com/hc/en-us/articles/9183454957074-All-about-currencies
[a-cantimport]: https://support.budgetbakers.com/hc/en-us/articles/7152318099346-Unable-to-import-my-files
