# DEPLOY.md

> Plan de déploiement d'Argos. Aucune valeur secrète ici : noms de variables seulement. Voir le skill `deploy-coolify-cloudflare`.

## Cible

- **Plateforme** : Coolify, sur le VPS Netcup (`152.53.176.13`).
- **Source Git** : `github.com/fs0ciety7000/argos` (privé), branche de production `main`. Pas de préproduction en V1.
- **Mode de build** : Dockerfile (`./Dockerfile` : node → go → alpine, utilisateur `argos` (UID 65532)).
- **Port d'écoute** : `8080`.
- **Route de santé** : `GET /health` → `200 {"status":"ok"}`.

> Compromis accepté en V1 : Argos tourne sur le VPS qu'il surveille. Si `netcup-01` tombe, le dashboard tombe avec lui.

## Variables

| Nom | Quand | Secret | Rôle |
|---|---|---|---|
| `ARGOS_ADDR` | exécution | non | adresse d'écoute (`:8080`, défaut de l'image) |
| `ARGOS_DATA_DIR` | exécution | non | dossier des données persistantes (`/data`, défaut de l'image) |
| `ARGOS_ADMIN_PASSWORD_HASH` | exécution | **oui** | hash argon2id (format PHC) du mot de passe, produit par `argos hash-password`. Contient des `$` : cocher « Is Literal » dans Coolify pour éviter l'interpolation. |
| `ARGOS_TOTP_SECRET` | exécution | **oui** | secret TOTP base32, produit par `argos totp-init` |
| `ARGOS_HOSTS` | exécution | non | inventaire JSON : `name`, `kind` (`vps`/`seedbox`), `address`, `port` (22 par défaut), `user`, `docker` (vps seulement). « Is Literal » coché. |
| `ARGOS_CF_ACCESS_TEAM_DOMAIN` | exécution | non | `cinecode.cloudflareaccess.com` : active la vérification du JWT Cloudflare Access |
| `ARGOS_CF_ACCESS_AUD` | exécution | non | tag d'audience de l'application Access « Argos (control center) » |
| `ARGOS_PROTECTED_CONTAINERS` | exécution | non | conteneurs qu'Argos ne redémarre jamais, séparés par des virgules ; `nom*` = préfixe (utile pour les conteneurs Coolify renommés à chaque déploiement). Le cœur de Coolify et le conteneur d'Argos sont toujours protégés. |

Sans ces deux variables, Argos démarre (santé OK) mais la connexion renvoie `503`. Une seule des deux, ou une valeur invalide : le démarrage échoue.

Générer les valeurs (dans ton terminal, jamais dans le chat) :

```bash
go run ./cmd/argos hash-password   # demande le mot de passe deux fois, sans l'afficher
go run ./cmd/argos totp-init   # affiche un QR code à scanner + la valeur pour Coolify
go run ./cmd/argos totp-code   # vérifie : affiche le code attendu pour un secret donné
```

Sessions en mémoire : un redéploiement déconnecte.

Les valeurs vivent dans la ressource Coolify et dans `secrets/` (chiffré). Jamais dans le dépôt. Aucune variable de build.

## Données

- **Volume persistant** : `/data` → base SQLite (`argos.db`, métriques 7 jours) et identité SSH d'Argos + empreintes des hôtes (`/data/ssh/`, dossier 0700, clé privée 0600). Propriétaire : utilisateur `argos` (UID 65532).
- **Sauvegarde** : copie quotidienne de `/data` (sauvegarde planifiée Coolify ou `sqlite3 .backup`) vers le stockage de backup ; test de restauration à chaque montée de schéma.

## SSH vers les hôtes

- Au premier démarrage, Argos génère sa propre clé ed25519 dans `/data/ssh/`. La clé privée ne quitte jamais le volume ; la clé publique s'affiche dans **Hôtes** : l'ajouter à `~/.ssh/authorized_keys` de l'utilisateur configuré sur chaque hôte.
- Empreintes des serveurs : enregistrées au premier contact dans `/data/ssh/known_hosts`. Si une empreinte change, Argos refuse la connexion (« clé d'hôte modifiée »), même après « Reconnecter ». Après une réinstallation légitime : retirer la ligne de l'hôte, puis « Reconnecter ».
- Utilisateur sur les VPS : `argos`, **jamais dans le groupe `docker`** (équivaut à root). Son seul droit élevé est le script gardien ci-dessous.
- Netcup (même machine qu'Argos) : adresse `152.53.176.13` ; le pare-feu doit laisser le conteneur joindre le port SSH de l'hôte.

## Docker sur les VPS (script gardien)

Argos ne parle à Docker qu'à travers `deploy/argos-docker`, un script appartenant à root qui n'accepte que `list` (champs fixes, jamais l'environnement ni les volumes), `stats`, `restart <nom>` (nom exact, cœur de Coolify refusé, redémarrage par ID complet) et `logs <nom> <1-1000>` (suivi des logs, 1 h max, 6 flux simultanés max, s'arrête à la fermeture du canal SSH ; logs du cœur de Coolify refusés car ils peuvent contenir des jetons). **Après chaque mise à jour du script dans le dépôt, le réinstaller sur les VPS** (même commande `install`, la ligne sudoers ne change pas). Installation sur chaque VPS Docker, en root :

```bash
install -o root -g root -m 0755 argos-docker /usr/local/sbin/argos-docker
echo 'argos ALL=(root) NOPASSWD: /usr/local/sbin/argos-docker' > /etc/sudoers.d/argos && chmod 0440 /etc/sudoers.d/argos && visudo -c
```

- Ne pas ajouter `SETENV`, `env_keep` ni de joker à cette ligne sudoers.
- Puis passer `"docker": true` sur l'hôte dans `ARGOS_HOSTS` et redéployer.
- Redémarrage automatique : opt-in par conteneur avec le label Docker `argos.autorestart=true`. Conteneur arrêté sur erreur ou `unhealthy`, confirmé sur 2 relevés, 3 tentatives par heure maximum (espacées de 1, 2, 4 min), puis abandon jusqu'à 1 h de bon fonctionnement ou un redémarrage manuel. Chaque action va au journal.
- Limite connue : une application qui sort en erreur sur un `docker stop` volontaire peut être relancée si elle porte le label.

## Domaine

- **FQDN** : `argos.fs0ciety.org`
- **Zone Cloudflare** : `fs0ciety.org` (`7c749c5d4c715e429122addb7f3829d7`)
- **Enregistrement DNS à créer** : type `A`, nom `argos`, cible `152.53.176.13`, proxifié **oui** (WebSocket supporté par le proxy).
- **SSL/TLS** : Full (strict), certificat Let's Encrypt émis par Coolify à l'origine. Jamais « Flexible ».
- **Cloudflare Access** : application auto-hébergée sur `argos.fs0ciety.org`, politique « Allow » limitée à l'e-mail du propriétaire. Couche en plus de l'authentification d'Argos, pas à la place. `/health` reste derrière Access (la santé Coolify passe en interne).
- **Vérification à l'origine** : avec `ARGOS_CF_ACCESS_*` définies, Argos refuse (`403`) toute requête sans JWT `Cf-Access-Jwt-Assertion` valide (signature RS256 via `https://<team>/cdn-cgi/access/certs`, `iss`, `aud`, `exp`). Un accès direct à l'IP du VPS est donc bloqué. Seul `/health` est exempté.

## Mise en production

1. Tests, lint et build passent.
2. Variables attendues présentes (noms vérifiés dans Coolify).
3. Application Coolify créée avec les valeurs ci-dessus.
4. Enregistrement DNS créé, domaine ajouté dans Coolify, certificat émis.
5. Vérification : statut `running:healthy`, URL répond, logs récents sans erreur.

## Retour arrière

Redéployer le commit précédent depuis Coolify (« Redeploy » sur l'ancien déploiement, ou `git revert` puis push sur `main`). Si le schéma SQLite a changé : arrêter l'application, restaurer `/data/argos.db` depuis la dernière sauvegarde, redémarrer.
