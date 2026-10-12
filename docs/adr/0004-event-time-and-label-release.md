# Everything runs on Event Time, and Labels are only visible after a delayed Label Release

Windows, ordering and Label visibility all use Event Time; wall-clock time is used only to measure latency. Labels become visible to features (for example "released fraud Labels in this Component") only at `event_time + delay` (default 30 days for IEEE-CIS, 5 timesteps for Elliptic). This stands in for chargeback lag. Without it, the graph features would leak the answer and the headline graph lift would be inflated.
