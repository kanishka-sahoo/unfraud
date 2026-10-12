# unfraud

A real-time fraud detection engine. It scores payment and transfer events as they arrive, links entities into a graph to surface coordinated fraud rings, and turns each flag into a case for an analyst to review. Every published number is reproducible from public datasets.

## Language

### Data

**Profile**:
The full description of one dataset as the engine sees it: how raw rows become Events, which Entities exist, how they link, and which features, rules and model apply.
_Avoid_: Tenant, domain, dataset config

**Feature Schema**:
The ordered list of numeric and categorical feature names a Profile's Events carry. Events send feature values by position in this list.
_Avoid_: Feature set, columns

**Event**:
One scorable unit in the standard schema, carrying an event time, Entities and features. In IEEE-CIS and PaySim this is a payment; in Elliptic it is a Bitcoin transaction node.
_Avoid_: Transaction (ambiguous across profiles), message, record

**Entity**:
A thing that Events refer to and that features are counted against, such as a Card UID, a Device Fingerprint, an email domain or an address.
_Avoid_: Account (except where a Profile genuinely has accounts), key

**Card UID**:
The reconstructed identity of a card in IEEE-CIS, derived from card, address and day-offset columns, because the dataset has no explicit card or customer ID.
_Avoid_: Card ID, user ID, customer

**Device Fingerprint**:
The combination of device and browser identity attributes that stands in for a device in IEEE-CIS. The dataset has no raw IP.
_Avoid_: Device ID, IP

**Event Time**:
When an Event happened according to the dataset. All windows, Label Releases and orderings use it; wall-clock time is used only to measure system latency.
_Avoid_: Timestamp (ambiguous), processing time

**Watermark**:
A declaration that no more Events or Links at or before a given Event Time will arrive.
_Avoid_: Checkpoint, barrier

### Scoring

**Provisional Score**:
The score given to an Event on arrival, using only what was known at its Event Time.
_Avoid_: Initial score, real-time score

**Revision**:
A later re-score of an Event, triggered by new graph structure such as a closed Elliptic timestep or a Ring Alert.
_Avoid_: Rescore, update

**Decision**:
The outcome for an Event at a given moment: approve, review or block. A Decision can be degraded when a dependency is unavailable.
_Avoid_: Verdict, action

**Alert Budget**:
The fixed share of Events the engine may flag for review. Thresholds are set to meet it, and recall is measured at it.
_Avoid_: Threshold (that is how the budget is enforced, not the budget itself)

**Reason Code**:
A stable identifier for why a rule fired.
_Avoid_: Rule name, explanation

**Contribution**:
The per-feature SHAP value explaining a model score.
_Avoid_: Feature importance (that is global, not per Event)

**Shadow Model**:
A model that scores live Events alongside the active model; its Decisions are recorded but never acted on.
_Avoid_: Canary, challenger

### Graph

**Link**:
An edge between Entities or Events. An attribute Link is derived from a shared attribute value (IEEE-CIS); a flow Link comes from the dataset (Elliptic).
_Avoid_: Edge (fine in code, not in the domain), relation

**Super-node**:
An attribute value shared by so many Entities (for example `gmail.com`) that linking through it would merge unrelated activity. Super-nodes are excluded from linking.
_Avoid_: Hub, popular node

**Component**:
A connected set of Entities within the sliding graph window.
_Avoid_: Cluster, group

**Community**:
A densely connected subset of a Component, found by periodic community detection.
_Avoid_: Cluster, partition

**Ring**:
A Community judged suspicious enough to raise a Ring Alert.
_Avoid_: Fraud ring (redundant), gang, network

**Ring Alert**:
The signal that a Ring has been detected or has grown. It produces a Case and can trigger Revisions for its member Events.
_Avoid_: Graph alert

### Labels and review

**Label**:
The dataset's ground truth for an Event (fraud or legitimate; unknown in Elliptic).
_Avoid_: Tag, class

**Label Release**:
The moment a Label becomes visible to the engine. It lags the Event Time by a fixed delay that stands in for chargeback lag; before release, nothing may use the Label.
_Avoid_: Label arrival, feedback

**Case**:
The unit of analyst work: either one flagged Event or one Ring.
_Avoid_: Alert, ticket, incident

**Case Packet**:
The structured facts about a Case that a Summary is allowed to draw on.
_Avoid_: Context, prompt

**Summary**:
A short narrative of a Case written by an LLM from its Case Packet.
_Avoid_: Explanation, report

**Grounding**:
The property that every number and Entity in a Summary appears in its Case Packet. Summaries that fail the check are not shown.
_Avoid_: Faithfulness (fine as a metric name), accuracy

**Disposition**:
An analyst's judgment on a Case (confirmed fraud, dismissed, or escalated). It is never treated as a Label.
_Avoid_: Label, feedback, resolution

### Benchmarking

**Replay**:
Feeding a Profile's dataset into the engine in Event Time order at a chosen speed.
_Avoid_: Simulation, backfill

**Accuracy Run**:
A deterministic Replay in which every feature is exact as of Event Time. All published fraud metrics come from Accuracy Runs.
_Avoid_: Offline eval, batch mode

**System Run**:
A Replay in the production topology at full speed. It measures throughput, latency and the score drift that stale features cause.
_Avoid_: Load test (it is one, but it also measures drift)
