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
| `ARGOS_ADMIN_PASSWORD_HASH` | exécution | **oui** | hash argon2id du mot de passe du compte unique *(à ajouter avec la fonctionnalité d'authentification)* |
| `ARGOS_SESSION_KEY` | exécution | **oui** | clé de signature des cookies de session, 32 octets aléatoires *(idem)* |

Les valeurs vivent dans la ressource Coolify et dans `secrets/` (chiffré). Jamais dans le dépôt. Aucune variable de build.

## Données

- **Volume persistant** : `/data` → base SQLite (`argos.db`, métriques 7 jours) et clés SSH de lecture vers les seedboxes (`/data/ssh/`, mode 0600). Propriétaire : utilisateur `argos` (UID 65532).
- **Sauvegarde** : copie quotidienne de `/data` (sauvegarde planifiée Coolify ou `sqlite3 .backup`) vers le stockage de backup ; test de restauration à chaque montée de schéma.

## Domaine

- **FQDN** : `argos.fs0ciety.org`
- **Zone Cloudflare** : `fs0ciety.org` (`7c749c5d4c715e429122addb7f3829d7`)
- **Enregistrement DNS à créer** : type `A`, nom `argos`, cible `152.53.176.13`, proxifié **oui** (WebSocket supporté par le proxy).
- **SSL/TLS** : Full (strict), certificat Let's Encrypt émis par Coolify à l'origine. Jamais « Flexible ».
- **Cloudflare Access** : application auto-hébergée sur `argos.fs0ciety.org`, politique « Allow » limitée à l'e-mail du propriétaire. Couche en plus de l'authentification d'Argos, pas à la place. `/health` reste derrière Access (la santé Coolify passe en interne).

## Mise en production

1. Tests, lint et build passent.
2. Variables attendues présentes (noms vérifiés dans Coolify).
3. Application Coolify créée avec les valeurs ci-dessus.
4. Enregistrement DNS créé, domaine ajouté dans Coolify, certificat émis.
5. Vérification : statut `running:healthy`, URL répond, logs récents sans erreur.

## Retour arrière

Redéployer le commit précédent depuis Coolify (« Redeploy » sur l'ancien déploiement, ou `git revert` puis push sur `main`). Si le schéma SQLite a changé : arrêter l'application, restaurer `/data/argos.db` depuis la dernière sauvegarde, redémarrer.
