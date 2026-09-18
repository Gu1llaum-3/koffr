# ADR-0002 — La pile est Go 1.27, sans CGO, avec les bibliothèques de l'annexe du CDC

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-009`, `E-027`, `E-117`, `E-118`, `E-119`, `E-121`, `E-123`
- **Références** : tranche `D-07` ; CDC § 12 (annexe, « indicative, à confirmer ») ; lié à ADR-0005

## Contexte

Le CDC impose Go (en-tête : « Go 1.23+ ») et donne une pile **indicative**, « à confirmer au moment
d'écrire le `go.mod` », avec un critère de sélection unique et ferme : la compatibilité avec
`CGO_ENABLED=0` (`N1`, soit `E-117`). Deux points de cette annexe ne sont pas indicatifs mais
contraignants, parce que d'autres exigences en dépendent : `modernc.org/sqlite` (`E-027`, et
l'interdiction explicite de `mattn/go-sqlite3`, qui impose CGO) et `filippo.io/age` (`E-075` : une
archive doit rester déchiffrable par l'outil `age` standard).

Le dépôt déclare déjà `go = "1.27"` dans `mise.toml`, sans trace de décision. Le CDC dit « 1.23+ » :
il n'y a pas de contradiction, mais un choix posé sans écrit.

koffr est distribué en binaire statique et en image conteneur (`E-117`, `E-124`) : personne ne le
compile avec une chaîne ancienne, et la directive `toolchain` de Go télécharge la version requise au
besoin.

## Décision

Go **1.27** pour l'outillage comme pour `go.mod`, `CGO_ENABLED=0`, et la pile de l'annexe § 12
confirmée telle quelle.

- `mise.toml` fait autorité sur la version d'outillage ; `go.mod` déclare `go 1.27`.
- Bibliothèques retenues, avec la raison de chacune : `modernc.org/sqlite` (pur Go, `E-027`),
  `filippo.io/age` (format standard, `E-075`), `klauspost/compress/zstd`, `aws/aws-sdk-go-v2`
  (endpoint personnalisé pour les S3 tiers, `E-069`), `pkg/sftp` + `x/crypto/ssh`, `robfig/cron/v3`
  (cinq champs, fuseaux, `E-088`), `spf13/cobra`, `gopkg.in/yaml.v3` avec `KnownFields(true)`
  (indispensable à l'analyse stricte de `E-032`), `jackc/pgx/v5` et `go-sql-driver/mysql` pour les
  sondes et la détection de famille **jamais pour le dump**, `docker/docker/client` (stratégie `exec`
  seule), `log/slog` (`E-121`), `embed` + HTMX embarqué (`E-098`),
  `testcontainers/testcontainers-go` en dépendance de test uniquement (`E-123`).
- Toute dépendance nouvelle passe un test : compile-t-elle sans CGO, et fait-elle grossir le binaire
  au-delà des 30 Mo de `E-117` ? La taille du binaire est mesurée par la CI et bloque au seuil.
- Outillage de qualité : `golangci-lint` (avec `depguard` et `forbidigo` pour les frontières
  d'architecture, voir ADR-0010), `gofumpt`, `go vet`. La commande `verify` regroupe l'ensemble.

## Conséquences

- L'interdiction de CGO est un filtre permanent : elle exclut par avance toute bibliothèque qui
  encapsule une bibliothèque C, et elle doit être vérifiée en CI (`CGO_ENABLED=0 go build`) plutôt
  que rappelée dans un document.
- `pgx` et `go-sql-driver/mysql` ne servent qu'aux sondes : le dump reste un sous-process. Une règle
  de lint doit empêcher qu'un jour un développeur « optimise » en lisant les tables par le pilote.
- L'empaquetage par une distribution (Debian, Fedora) qui reconstruirait depuis les sources avec un
  Go plus ancien n'est pas garanti. Acceptable : on publie des binaires et une image.
- La cible des 30 Mo est serrée avec le client Docker et le SDK AWS. À mesurer dès le lot 0 ; si
  elle ne tient pas, c'est `E-117` qu'on rouvre par un nouvel ADR, pas la dépendance qu'on bricole.
- **Ce qui rouvrirait la décision** : un dépassement durable de 30 Mo, ou l'abandon de
  `CGO_ENABLED=0` (qui invaliderait `E-009` et une partie de l'argumentaire produit).

## Alternatives écartées

- **`go.mod` à 1.23, outillage à 1.27** : utile seulement pour un empaqueteur de distribution, qui
  n'est pas un canal de diffusion retenu.
- **Tout ramener à 1.23** : demanderait de redescendre `mise.toml`, sans contrepartie.
- **`mattn/go-sqlite3`** : interdit par le CDC, casse `E-117`.
- **Une base embarquée non SQLite (bbolt, Pebble)** : le CDC nomme SQLite et l'état est relationnel
  (sept tables, jointures de catalogue). Pas de raison de dévier.
