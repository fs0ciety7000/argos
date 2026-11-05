# AGENTS.md

Règles communes à tous les agents de code (Claude Code, Codex, Cursor...). Format neutre : rien ici n'est propre à Claude.

## Contexte

- **Nom** : Argos
- **Type** : backend Go + frontend Svelte 5 (dashboard DevOps)
- **Résumé en une phrase** : panneau de contrôle auto-hébergé qui surveille en temps réel VPS et seedboxes via SSH (métriques, conteneurs Docker, logs, stockage).

## Commandes

```bash
install: make install          # npm ci dans web/
dev:     make dev-api & make dev-web   # Go sur :8080, Vite proxifie /api et /health
test:    make test             # go test ./...
lint:    make lint             # go vet + svelte-check
build:   make build            # build Vite puis binaire bin/argos (front embarqué)
```

## Structure du dépôt

```
.
├── AGENTS.md
├── CLAUDE.md
├── DESIGN.md           # direction visuelle
├── DEPLOY.md           # Coolify / Cloudflare, domaine, variables (noms)
├── docs/brief.md       # objectif et périmètre
├── .env.example        # noms des variables, valeurs factices
├── secrets/            # secrets chiffrés (sops + age), commitables
├── Dockerfile          # node → go → alpine (non root), volume /data
├── Makefile
├── cmd/argos/          # point d'entrée du binaire
├── internal/server/    # routes HTTP (/health, API, UI embarquée)
└── web/                # app Svelte 5 + Vite ; embed.go expose web/dist
```

## Règles de travail

1. Lis avant d'écrire : ouvre les fichiers concernés avant de les modifier.
2. Plus petit changement qui résout le problème. Pas de refactor non demandé.
3. Suis le style existant (nommage, formatage, structure).
4. Ajoute ou mets à jour les tests avec chaque changement de comportement.
5. Lance `test` et `lint` avant de conclure ; rapporte les résultats réels.
6. Ne touche jamais à : `.env*`, `dist/`, `build/`, fichiers de lock modifiés à la main.
7. Tâche de plus de ~5 fichiers ou changement d'architecture : propose d'abord un plan.

## Sécurité

- Aucun secret dans le code, les logs ou les commits. Les secrets passent par `kit secrets`.
- Valide toutes les entrées externes ; requêtes SQL paramétrées uniquement.
- Confirmation avant toute action destructive (suppression, reset, migration ou déploiement en prod).
