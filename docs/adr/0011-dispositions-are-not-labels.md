# Analyst Dispositions are never treated as Labels

The web app lets an analyst confirm, dismiss or escalate a Case. Those Dispositions are stored and shown, but they never become Labels, never feed features or training, and never enter metrics. Dataset Labels are the only ground truth. Otherwise anyone clicking through the UI could change the published numbers, and results would stop being reproducible.
