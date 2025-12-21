# Gas Town

Multi-agent workspace manager for AI coding agents.

Gas Town coordinates swarms of AI agents working on software projects. Workers (polecats) implement features and fix bugs. Refineries review and merge code. Witnesses manage worker lifecycles. Mayors coordinate across projects.

## Install

```bash
go install github.com/steveyegge/gastown/cmd/gt@latest
```

## Quick Start

```bash
# Create a town (workspace)
gt install ~/gt

# Add a project rig
gt rig add myproject --remote=https://github.com/you/myproject.git

# Spawn a worker on an issue
gt spawn --issue myproject-123
```

## Architecture

```
Town (~/gt/)
├── Mayor (global coordinator)
└── Rig: myproject
    ├── Witness (lifecycle manager)
    ├── Refinery (merge queue)
    └── Polecats (workers)
```

## Key Concepts

- **Molecules**: Structured workflow templates with quality gates and dependencies
- **Beads**: Unified data plane for issues, messages, and state ([github.com/steveyegge/beads](https://github.com/steveyegge/beads))
- **Nondeterministic Idempotence**: Workflows survive crashes and agent restarts

## Commands

```bash
gt status             # Town status
gt rig list           # List rigs
gt spawn --issue <id> # Start worker
gt mail inbox         # Check messages
```

## Documentation

- [Architecture](docs/architecture.md)
- [Molecules](docs/molecules.md)
- [Federation](docs/federation-design.md)

## Development

```bash
go build -o gt ./cmd/gt
go test ./...
```

## License

MIT
