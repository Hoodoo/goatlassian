# Files

- [Architecture Overview](overview.md) - How goatlassian's packages fit together, how data flows from kata, owcli, bossman, and git into a project report, and the rules the design follows.
- [Tool Adapters and Discovery](tool-adapters.md) - The commands goatlassian runs against kata, owcli, bossman, and git, what it reads from each, how failures surface, the deep-link formats, and how discovery maps tool records onto projects.
- [Web UI and JSON API](web-ui.md) - How goatlassian serve works - its JSON API, request guards, World caching, the snapshot loop, and the embedded single-page client.
