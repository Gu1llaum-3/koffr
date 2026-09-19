# Recette lot 1 — Diagnostic d'un parc réel

Ce lot ne sauvegarde toujours rien, et c'est voulu. Il répond à une seule question, mais c'est la
question dont dépend tout le reste : **sur ce parc, quel outil sauvegardera quelle base, et
comment le sait-on ?**

C'est la première recette qui demande de **vraies bases**. Un jeu de données inventé ne prouverait
rien : la thèse du produit est qu'il dit juste sur un parc hétérogène réel.

Durée attendue : quarante minutes, préparation des bases non comprise.

## Avant de commencer

- Une machine avec `mise` et **Docker**, sans Go préinstallé. Prévoir plus de 2 Go de mémoire : les
  conteneurs de bases ne tiennent pas dans l'instance du lot 0.
- **Trois bases réelles, de versions et de familles différentes**, joignables depuis cette machine :
  PostgreSQL 16, MariaDB 11.4, MySQL 8.4. Des conteneurs font l'affaire.
- Une quatrième base **éteinte**, déclarée dans la configuration : on vérifie aussi ce que koffr dit
  quand ça ne répond pas.
- Le dépôt cloné sur `main`, `mise run verify` vert.
- Prépare : le propriétaire.

## Ce qui est voulu et pourrait passer pour un bug

| Constat | Pourquoi c'est voulu |
| --- | --- |
| `doctor` n'affiche que trois champs par base, pas les sept du § 5.12 | `E-104b` est au lot 6 : mode de tampon, destinations, prochaine exécution et dernière sauvegarde supposent des lots qu'on n'a pas |
| Aucune sauvegarde n'est possible | C'est le lot 2 |
| `doctor` n'a pas de `--json` | `N-5` : `E-103a` n'en demande pas, et le figer maintenant contraindrait le lot 6 |
| Le cache de versions ne survit pas à la commande | `N-4` : `E-039` demande un cache et son invalidation, pas une persistance |
| `koffr tools install` échoue sur macOS | ADR-0011 et `N-7` : le spike ne vaut que pour ELF |
| `koffr tools list` montre des outils que koffr n'a pas installés | `N-6` : c'est ce qui rend `E-038` observable — l'énumération voit **toutes** les sources |
| La détection MyISAM n'apparaît nulle part | Elle sert à la politique de tampon, au lot 2 |
| `doctor` n'affiche jamais la provenance `container` | La stratégie `exec` est implémentée et testée, mais n'est câblée dans aucune commande : elle le sera au lot 2, quand la sauvegarde l'utilisera |

## Parcours

### 1. koffr voit le parc tel qu'il est

1. Déclarer les quatre bases dans `koffr.yaml`, puis `koffr doctor` → **on doit voir** une ligne par
   base, avec pour chacune sa **joignabilité**, la **version de son serveur** et l'**outil retenu**
   avec sa version et sa provenance.
2. Comparer chaque version annoncée à ce que la base répond vraiment (`SELECT version()`) →
   **on doit voir** la même chose. Une version fausse ici est un défaut **bloquant** : c'est la
   promesse `P3`.
3. La base éteinte → **on doit voir** une ligne qui la dit injoignable, et **les trois autres
   lignes quand même**. `doctor` diagnostique un parc, il ne s'arrête pas au premier problème.
4. `koffr doctor --database <id>` → **on doit voir** cette base seule ; avec un identifiant inconnu,
   une erreur qui **nomme les identifiants connus**.

**Décision attendue** : les trois champs suffisent-ils à décider d'agir, ou manque-t-il quelque
chose que vous regarderiez avant une nuit de sauvegarde ?

### 2. La version est prouvée, jamais devinée

1. `koffr tools list` → **on doit voir** tous les candidats trouvés : chemins système, `PATH`,
   répertoire géré, et `pg_lsclusters` si la machine est une Debian.
2. Renommer un binaire pour mentir sur sa version — copier `pg_dump` 15 en `pg_dump-16` — et
   relancer → **on doit voir** **15**, pas 16. La version vient de l'exécution, pas du nom.

**Décision attendue** : aucune. C'est la démonstration de `P3`.

### 3. Le piège du § 5.2

Le cahier des charges nomme ce bug parce que les outils concurrents le font.

1. Installer sur l'hôte le client PostgreSQL **14**, garder la base en **16**, et disposer d'un
   outil géré en **16**.
2. `koffr doctor` → **on doit voir** le **16** retenu, avec la provenance `managed`. Si koffr choisit
   le 14 parce qu'il est sur l'hôte, c'est **bloquant** : la provenance ne doit départager que des
   candidats également compatibles.
3. Ajouter un client 17 et 18, garder la base en 16 → **on doit voir** le **16** retenu, pas le 18 :
   la version la plus proche par le haut.

**Décision attendue** : aucune. C'est le critère de sortie n° 2.

### 4. Les familles ne se croisent jamais

1. Sur une machine où seul `mysqldump` d'Oracle est présent, viser la **MariaDB** → **on doit voir**
   un échec qui nomme la famille attendue. Un repli sur l'outil de l'autre famille est **bloquant**.
2. Inversement, `mariadb-dump` seul face à la **MySQL** → même refus.
3. Vérifier que la famille n'est **écrite nulle part** dans `koffr.yaml` : elle est détectée à la
   connexion (`E-041`).

**Décision attendue** : aucune. C'est le critère de sortie n° 6.

### 5. Un échec dit quoi faire

1. Une base PostgreSQL **17** avec seulement le client **15** installé → **on doit voir** un échec
   nommant la version attendue, les versions trouvées, et **la commande exacte** pour corriger
   (`E-042`). C'est le **scénario 2 du § 8**.
2. Lancer cette commande — `koffr tools install postgresql 17` — puis relancer → **on doit voir** la
   base réussir, **sans avoir touché la configuration**. C'est le **scénario 3 du § 8**.

**Décision attendue** : le message est-il actionnable pour quelqu'un qui ne connaît pas koffr ?
Copiez-le tel quel dans un moteur de recherche : trouve-t-on de quoi s'en sortir ?

> **Si la vague 6 n'a pas été exécutée** — `D-06` et `Q-15` non tranchées — ce parcours s'arrête au
> point 1, et le point 2 est **reporté**, avec sa décision écrite.

### 6. La configuration signale ce qui ne va pas

1. `koffr config validate` avec la base éteinte → **on doit voir** l'injoignabilité signalée et un
   code de retour non nul (`E-034`).
2. Une base dont aucun outil compatible n'existe → **on doit voir** l'outil manquant signalé.
3. `koffr config validate --offline` → **on doit voir** la vérification de forme seule, sans
   toucher au réseau.
4. Vérifier qu'aucune requête autre que la version n'a été émise vers les bases — journaux du
   serveur à l'appui. `F1.3` dit « sans rien exécuter d'autre ».

**Décision attendue** : `validate` et `doctor` se recouvrent en partie. Est-ce confortable, ou
faut-il que l'un renvoie à l'autre ?

### 7. La stratégie `exec`

1. Déclarer une base avec `tools: {strategy: exec, container: <nom>}` → **on doit voir** `doctor`
   annoncer l'outil trouvé **dans le conteneur**, avec la provenance `container`.
2. Arrêter le socket Docker et relancer → **on doit voir** un échec qui **nomme le socket attendu**.
3. Déclarer `strategy: exec` **sans** `container` → **on doit voir** le refus **à la validation de
   configuration**, pas au premier job.

**Décision attendue** : aucune.

## Décisions attendues de la session

1. **`D-06`** — qui construit, signe et héberge les binaires d'outils ? Si elle est encore ouverte à
   la recette, le lot se clôt sans la vague 6 et `E-043`, `E-044`, `E-045` et `E-131` sont
   reportées par une décision écrite.
2. **`Q-15`** — quelle signature protège ces archives, avec quelle clé ?
3. **`D-01` rouverte** — l'instance de recette doit désormais porter Docker et plusieurs bases. Le
   dispositif du lot 0 ne suffit plus ; qui fournit le parc, et pour combien de temps ?
4. La lisibilité de `doctor` (parcours 1) et l'actionnabilité des messages d'échec (parcours 5).

## Comment rapporter

Une anomalie = écran ou commande, geste, attendu, constaté, gravité (bloquant / gênant /
cosmétique). Dans `docs/recette/anomalies.md`, numérotée à la suite de `A-08`.
