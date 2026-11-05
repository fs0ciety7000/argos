# DESIGN.md

> Direction visuelle d'Argos : **Forge — graphite & signal**. Lue par les agents de design et de front. Maquette de référence : artboard « C · Forge » (https://claude.ai/artifact/KPM5Pb2U4fBYZtreBc37Wz, privé).

## Principes

1. **Voir en 2 secondes ce qui ne va pas, puis creuser.** Le calme est l'état normal ; seul ce qui dévie prend de la couleur.
2. **Premium, épuré, industriel.** Graphite profond, filets fins, une seule couleur d'accent, grands chiffres fins. Pas de néon, pas de glassmorphism, pas de dégradé de fond.
3. **Le mouvement porte la donnée.** L'animation montre qu'une valeur est vivante ou change ; jamais de décor gratuit.
4. **Statut codé deux fois.** Couleur + glyphe (● ▲ ■ ◌) + libellé ; la couleur seule ne suffit jamais.
5. **L'accent n'est jamais un statut.** Il sert à l'interface (sélection, action principale, série principale), jamais à dire ok/warn/critical.

## Couleurs

Tokens CSS (`web/src/app.css`, `:root`). Sombre = thème principal ; clair = variante minimale.

| Rôle | Token | Sombre | Clair |
|---|---|---|---|
| Fond de page | `--bg` | `#0B0C0E` | `#F4F4F2` |
| Surface (panneaux) | `--surface` | `#111317` | `#FFFFFF` |
| Surface creuse (logs, état figé) | `--surface-sunk` | `#08090B` | `#EDEDEA` |
| Remplissage (pistes de barres, onglet actif) | `--fill` | `#1A1D22` | `#E6E6E2` |
| Filet | `--line` | `#1F2329` | `#DCDCD7` |
| Filet survol / pointillé | `--line-strong` | `#2C313A` | `#C8C8C2` |
| Texte principal | `--text` | `#EDEEF0` (16:1) | `#111317` (16,9:1) |
| Texte secondaire | `--text-2` | `#8D939C` (6:1) | `#5A6069` (5,8:1) |
| Neutre data (série secondaire, barres) | `--steel` | `#9AA3AE` | `#5F656E` |
| Accent | `--accent` | `#FF6A1A` (6,5:1) | `#C2410C` (4,7:1) |
| Texte sur accent | `--on-accent` | `#0B0C0E` (6,8:1) | `#FFFFFF` (5,2:1) |
| Succès / ok ● | `--ok` | `#3DDC97` | `#0F7A54` |
| Alerte / warn ▲ | `--warn` | `#F5C518` | `#8A6400` |
| Danger / critical ■ | `--crit` | `#FF5468` | `#C8283E` |
| Inconnu / figé ◌ | `--unknown` | `#9AA3AE` | `#5F656E` |

- Contrastes mesurés (WCAG 2.x) sur `--surface` ; tous ≥ 4,5:1 pour le texte.
- Teintes de statut en fond : couleur + alpha `14` (fond de pastille), `33` (bordure), `10` (ligne en échec). Ex. `#3DDC9714`.
- Accents alternatifs validés (même rôle, à choisir une fois) : vert acide `#C6F432`, bleu ion `#5B8CFF`.
- Séries de graphes : 1 = `--accent` (trait plein + aire en dégradé vertical 28 % → 0), 2 = `--steel` (pointillé 4/4), 3+ = `#5B8CFF`, `#D6C18F`. Jamais une couleur de statut comme série.
- Seuils : trait fin `--text` à 85 % (pointillé) ou repère vertical de 1 px sur les barres.

## Typographie

- **Interface et titres** : `"Geist", system-ui, sans-serif` (OFL, Google Fonts), 400 / 500 / 600.
- **Données, valeurs, logs, horodatages** : `"Geist Mono", ui-monospace, Menlo, monospace`, `font-variant-numeric: tabular-nums slashed-zero`.
- **Grands chiffres** : Geist 300, `letter-spacing: -0.04em`, `line-height: 1`.
- **Échelle (px)** : 11 (axes) · 12 (méta, mono) · 13 (libellés) · 14 (corps) · 15 (titre de panneau, 500) · 16 (marque, 600) · 40 (valeur d'hôte) · 48 (valeur de carte) · 64 (valeur héro).
- Téléphone : corps 14 px minimum, valeur héro 48 px.

## Espacements, rayons, ombres

- Unité 4 px : 4 · 8 · 12 · 16 · 20 · 24 · 32 · 48. Gouttière entre panneaux 20 px ; padding de panneau 20–24 px ; page 32 px (16 px sur téléphone).
- Rayons : panneau 18 px · barre d'en-tête 14 px · bouton / onglet 10 px · lien de nav 8 px · pastille et barre de progression 99 px.
- **Pas d'ombre.** L'élévation se lit par le niveau de surface et le filet ; au survol, filet `--line-strong` et `translateY(-2px)`.
- Fond du panneau héro : trame de 32 px, trait `--text` à 4,5 % d'opacité.
- Largeur max du contenu : 1440 px.

## Composants

- **Barre d'en-tête** : logo (anneau qui tourne + iris plein en accent) · nav en liens arrondis (actif = `--fill`) · indicateur `LIVE · 12 ms` (point accent qui pulse) · sélecteur de plage segmenté (15m 1h 24h 7j).
- **Carte héro de graphe** : libellé `--text-2` 13 px, valeur 64 px, légende à droite, graphe 220 px avec aire dégradée, dernier point marqué et pulsant, axe temporel mono 11 px.
- **Carte d'hôte** : nom (15/500) + méta mono, pastille de statut à droite, valeur principale 40 px + sparkline 44 px, deux barres 4 px (RAM, disque / quota).
- **Pastille de statut** : mono 12 px, glyphe + libellé, fond `couleur14`, bordure `couleur33`, rayon 99 px.
- **Barre de stockage** : piste `--fill` 8 px, remplissage accent (ok) ou `--warn` / `--crit` au-delà du seuil, valeur `26.4 / 30.0 TB` et projection `plein dans ~39 j`.
- **Liste de conteneurs** : statut (largeur fixe), nom, uptime, mini-barre CPU, valeur ; la ligne en échec a un fond `--crit` à 6 % et le bouton d'action principal (accent).
- **Flux de logs** : surface creuse, mono 12,5 px, interligne 1,9 ; horodatage `--text-2`, niveau coloré + glyphe (`WRN ▲`, `ERR ■`), lignes anciennes progressivement estompées (opacité 0,4 → 1), messages d'Argos en accent.
- **Boutons** : principal = fond accent, texte `--on-accent`, 500 ; secondaire = `--fill` + filet `--line-strong` ; hauteur ≥ 36 px (≥ 44 px sur téléphone).

### États

| État | Rendu |
|---|---|
| Survol | filet `--line-strong`, panneau `-2px`, liens `--text-2` → `--text` (250 ms) |
| Focus clavier | `outline: 2px solid var(--text)`, `outline-offset: 2px`, toujours visible (`:focus-visible`) |
| Désactivé | opacité 0,4, curseur `not-allowed`, libellé explicite (« lecture seule ») |
| Chargement | squelettes `--fill` aux dimensions finales, balayage lent 1,6 s ; jamais de spinner plein écran |
| Vide | phrase courte + action (« Aucun conteneur sur cet hôte · Ajouter un hôte ») |
| Figé (WebSocket ou SSH muet) | carte en `--surface-sunk`, bordure pointillée, contenu à 45 %, pastille `◌ figé 34 s` qui compte, bouton « Reconnecter » |
| Erreur | pastille `■` + message exact (« ssh · délai dépassé (10 s) ») + action ; jamais de détail sensible (clé, chemin de clé) |
| Action en cours (restart) | statut `▲ relance`, bouton remplacé par compteur, retour automatique à `● actif` |
| Seedbox (lecture seule) | méta « Seedbox · lecture seule » ; aucune action affichée |

### Mobile (< 720 px)

Une colonne, panneaux pleine largeur, nav en défilement horizontal, sélecteur de plage sous la nav, tableaux dans une boîte `overflow-x: auto`, cibles tactiles ≥ 44 px, page 16 px de marge.

## Mouvement

- **Outils** : `svelte/motion` (`Spring`, `Tween`) pour les valeurs en direct (chiffres, barres, courbes) ; `svelte/transition` pour les entrées / sorties de listes ; CSS pour les boucles (pulse, rotation, balayage) ; **GSAP** seulement pour la mise en scène d'ouverture et le tracé des lignes. Pas de Motion (orienté React).
- **Courbes** : sortie `cubic-bezier(.2, .7, .2, 1)` ; tracé `cubic-bezier(.6, 0, .2, 1)` ; boucles `linear` ou `ease-in-out`.
- **Durées** : micro-interaction 250 ms · survol 350 ms · barre / valeur 800–1200 ms · entrée de panneau 800 ms en cascade de 80 ms · tracé de ligne 2 s · pulse 1,6 s · balayage 6 s · rotation du logo 9 s.
- **Données en direct** : la valeur glisse (Tween), ne « roule » pas ; le dernier point pulse ; une nouvelle ligne de log entre par le bas en fondu.
- **`prefers-reduced-motion: reduce`** : plus d'entrées, de boucles ni de survol en translation ; les valeurs changent sans interpolation ; le flux de données continue.

## Accessibilité

- Texte ≥ 4,5:1 (y compris les libellés gris 11–12 px) ; vérifier tout nouveau token avant usage.
- Graphes SVG : `role="img"` + `aria-label` qui résume la tendance ; décor en `aria-hidden="true"`.
- Vrais éléments : `<button>`, `<a href>`, `<nav aria-label>` ; `aria-pressed` sur le sélecteur de plage ; `aria-label` sur les boutons-icônes.
- Régions en direct : un résumé discret en `aria-live="polite"` pour les changements de statut, pas pour chaque tick.
