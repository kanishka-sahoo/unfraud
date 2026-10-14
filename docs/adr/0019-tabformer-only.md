# TabFormer is the only dataset

Supersedes the dataset choice in [ADR-0018](./0018-named-payment-datasets.md); its reasons for named, synthetic data stand.

The point of the project is the pipeline: a stream scored on arrival, revised by a graph service, turned into explained Cases, run on Kubernetes and checked against an offline replay. Three datasets mostly multiplied the work of showing that, and the cross-dataset question they were added to answer came out negative: models trained on one simulator scored at chance on the others, and pooling them gained nothing. So the repository keeps one dataset and shows it end to end.

TabFormer is the one that exercises every part:

- It has a graph worth drawing (cards linked through small merchants), so graph-svc, Revisions and the graph explorer all run.
- It has a Rule that survives validation (`FRAUD_PAYEE`), so Rules and the model both shape Decisions.
- Five years of card history make velocity features matter: real-time features lift test PR-AUC from 0.457 to 0.763.

## Considered Options

- **Sparkov:** small and fully replayable in an hour, but every merchant is shared by hundreds of cards, so it has no graph, and every Rule was retired. Half the system would sit idle.
- **AMLworld:** its feature state (5 GB for 500,000 accounts) does not fit the reference machine's cluster.
- **Keep all three:** more numbers, no more of the system shown, and three times the data, training and documentation.

## Consequences

- PaySim, Sparkov and AMLworld, the Payment Schema's pooling and transfer machinery (core-feature models, schema 0, `make pooled`) are removed; their results are in git history (commit c540a3d).
- The engine is still generic: Profiles, the feature engine, the graph and the services contain no TabFormer-specific code.
- A full live replay (9M Events) would take most of a day on the reference machine, mostly case-svc writing 18M Decisions to Postgres. The System Run therefore covers the first 1.5M Events; the published fraud metrics come from the Accuracy Run over all of them.
