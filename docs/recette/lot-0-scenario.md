# Recette lot 0 — Squelette, outillage et spike des outils

Ce lot ne produit **aucun écran** et aucune sauvegarde. Sa recette est technique : elle se joue dans
un terminal, sur une machine où le dépôt vient d'être cloné. Elle vaut quand même d'être jouée par
quelqu'un d'autre que celui qui a écrit le code — c'est la première fois qu'on vérifie que le projet
s'installe et se construit ailleurs que sur le poste de développement.

Durée attendue : vingt minutes.

## Avant de commencer

- Une machine avec `mise`, **sans Go préinstallé** (c'est `mise` qui doit l'apporter — on vérifie
  aussi cela). **Docker n'est pas nécessaire** au lot 0 : le spike a déjà tourné et le parcours 5
  se contente de lire son rapport. Il le sera au lot 1.
- Un compilateur C n'est **pas** requis non plus : `verify` s'en passe et le dit (`A-01`).
- Le dépôt `github.com/Gu1llaum-3/koffr` cloné, sur `main`.
- Aucune préparation de données : le lot n'en manipule pas.
- Prépare : le propriétaire.

## Ce qui est voulu et pourrait passer pour un bug

| Constat | Pourquoi c'est voulu |
| --- | --- |
| `koffr version` est la seule commande qui fait quelque chose | `N-1` : traversée minimale du lot 0. Toute la surface CLI est au lot 1 (`E-103a`) |
| `koffr config validate` accepte une configuration dont la base est injoignable et dont les outils n'existent pas | `E-034` est au lot 1. La commande lit bien les secrets qu'on lui désigne (`CFG-09`), mais n'ouvre aucune connexion |
| `internal/state` et `internal/egress` n'ont aucun appelant | `serve` est au lot 5, un émetteur réel aux lots 1, 6 et 8 |
| La base `koffr.db` contient sept tables dont cinq resteront vides | `N-9` : `E-028` est une exigence de ce lot ; les tables se remplissent aux lots 2 et suivants |
| Aucune sauvegarde n'est possible | C'est le lot. La première sauvegarde est au lot 2 |
| Il n'existe aucun binaire Windows | ADR-0011 : Windows est hors périmètre, et c'est annoncé |
| `koffr tools install` n'existe pas encore | Lot 1, et seulement sur Linux (ADR-0011, `Q-20`) |

## Parcours

### 1. Le projet se construit sur une machine neuve

1. `mise install` dans le dépôt → **on doit voir** Go 1.27.1 et `golangci-lint` installés par `mise`,
   sans avoir rien installé à la main.
2. `mise run verify` → **on doit voir** les quatre étapes (`check`, `lint`, `test`, `build`) passer,
   et un code de retour 0. Noter la durée. Sur une machine sans compilateur C, **on doit voir**
   `race detector: off` avec la raison et la façon de l'obtenir — et `verify` passe quand même
   (`A-01`). La CI, elle, l'exige.
3. Ouvrir la page des actions du dépôt sur GitHub → **on doit voir** la CI verte sur le dernier
   commit de `main`.

**Décision attendue** : la durée de `verify` est-elle acceptable pour la lancer à chaque vague ? Si
elle dépasse deux minutes dès maintenant, on le note : elle ne fera que croître.

### 2. Le binaire est bien ce qu'il prétend être

1. `mise run release` → **on doit voir** trois binaires dans `dist/`, pour `linux/amd64`,
   `linux/arm64` et `darwin/arm64`, et les contrôles de `E-117` passer. **Aucun binaire Windows** :
   c'est ADR-0011. (`mise run build`, lui, ne construit que la plateforme courante, pour itérer
   vite.)
2. Regarder la taille de chacun → **on doit voir** une taille bien inférieure à 30 Mo, et la CI
   l'afficher dans son journal.
3. `./dist/koffr-linux-arm64 version --json` → **on doit voir** un objet JSON avec le nom, la version, le commit, la
   date de construction, la version de Go et la plateforme. Aucun champ vide.

**Décision attendue** : aucune. C'est une vérification.

### 3. La configuration refuse ce qu'elle doit refuser

1. `koffr config validate --offline --config examples/koffr.yaml` sur la configuration livrée —
   celle du § 5.1 du cahier des charges → **on doit voir** `ok`, et rien d'autre. `--offline` parce
   que l'exemple désigne des chemins de production (`/run/credentials/…`) qui n'existent que sur un
   hôte koffr.
2. Remplacer `timezone:` par `timezon:` et relancer → **on doit voir** une erreur qui **nomme la clé
   fautive et sa ligne**. Une faute de frappe ne doit jamais désactiver silencieusement une
   sauvegarde : c'est l'exigence `E-032`, et c'est la raison d'être de ce contrôle.
3. Retirer complètement `agent.timezone` et relancer → **on doit voir** une erreur : le fuseau est
   obligatoire et ne s'hérite jamais du système (`E-036`).
4. Déclarer à la fois `password:` et `password_file:` pour la même base → **on doit voir** une
   erreur : une seule forme à la fois (`E-033`).
5. Pointer `password_file:` vers un fichier qui n'existe pas et relancer **sans** `--offline` →
   **on doit voir** l'erreur **tout de suite**, nommant le fichier (`CFG-09`). Relancer **avec**
   `--offline` → accepté, et la sortie dit que les secrets n'ont pas été lus.

**On doit voir**, dans chacun de ces messages : la **section**, la **clé** et la **ligne** — et
aucun nom de type Go (`A-05`).

**Décision attendue** : les messages d'erreur sont-ils compréhensibles par quelqu'un qui découvre
l'outil ? Ils sont en anglais (ADR-0003) : est-ce confortable pour vous à l'usage ?

### 4. Aucun secret ne fuit

1. Écrire une configuration avec un mot de passe littéral, un `*_env` et un `*_file`.
2. `koffr config show --redact` → **on doit voir** la topologie du parc — hôtes, ports, noms de
   bases — et **aucune valeur sensible**, sous aucune des trois formes (`E-115`).
3. `koffr version --log-dir /tmp/koffr-recette` puis lire `/tmp/koffr-recette/koffr.log` →
   **on doit voir** du JSON, une ligne par événement, et **aucun secret**. Le résultat de la
   commande, lui, sort sur la sortie standard : les deux flux sont séparés.
4. `koffr version --log-dir /proc/impossible` → **on doit voir** la commande **réussir** quand
   même : un journal impossible à écrire n'arrête pas koffr (`N-3`).

**Décision attendue** : la redaction va-t-elle assez loin ? Y a-t-il un champ que vous considérez
comme sensible et que nous affichons en clair ?

### 5. Le spike a répondu

1. Ouvrir `docs/inputs/spike-2026-09-rpath.md` → **on doit voir** ce qui a été tenté sur Debian,
   Rocky et Alpine, en `arm64` et en `amd64`, et une **conclusion explicite** : l'installation
   gérée d'outils (`E-043`) est tenable, ou ne l'est pas.
2. Si la conclusion est négative pour Alpine → **on doit voir** l'ADR qui amende `E-043`, écrit
   avant le lot 1.

**Décision attendue** : c'est la décision la plus lourde de cette session. Si le spike échoue sur
Alpine, acceptez-vous que l'installation gérée ne vaille que sur les distributions glibc, Alpine
étant renvoyée à la stratégie `exec` ou aux outils de l'hôte ? Et cela change-t-il votre position
sur `D-06` (qui construit et héberge ces binaires) ?

### 6. Les garde-fous mordent

1. Ajouter volontairement, dans un paquet du domaine, un import de `internal/state` ; lancer
   `mise run verify` → **on doit voir** l'échec, avec le nom de la règle violée (`AR-01`).
2. Retirer l'import.

**Décision attendue** : aucune. C'est la démonstration qu'une règle d'architecture écrite dans
`ARCHITECTURE.md` est vérifiée par l'outillage, et non par la bonne volonté.

## Historique

- **2026-09-18, première session** : jouée sur une instance Multipass. Sept anomalies, `A-01` à
  `A-07`, toutes tranchées ; corrigées par `docs/plans/lot-0-corrections.md`. Ce scénario a été
  corrigé par la même occasion (`A-02`, `A-04`), et rejoué en entier.

## Décisions attendues de la session

1. **`D-01`** — qui joue la recette des lots suivants, et sur quel parc réel ? À partir du lot 1, la
   recette demande de vraies bases PostgreSQL, MySQL et MariaDB, dans des versions différentes.
2. La suite du spike, si sa conclusion est négative (parcours 5).
3. La langue des messages : confirmée en anglais par ADR-0003, à confronter à l'usage réel
   (parcours 3).

## Comment rapporter

Une anomalie = écran ou commande, geste, attendu, constaté, gravité (bloquant / gênant /
cosmétique). Dans `docs/recette/anomalies.md`, numérotée `A-nn`.
