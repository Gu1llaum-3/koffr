# Règles — module `config` (code `CFG`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

## Lecture de `koffr.yaml`

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CFG-01 | Une clé que koffr ne connaît pas est une **erreur**, jamais un avertissement, et l'erreur **nomme la clé et sa ligne** — à la racine, dans une section, dans `databases[n]`, dans `destinations[n]` et sous `tools`. | `E-032`, § 5.1 `F1.1` | `parse_test.go › TestCFG01UnknownKeyIsAnErrorNamingTheKeyAndItsLine` |
| CFG-02 | Chaque champ sensible du § 5.1 accepte ses **trois formes** : valeur littérale, `*_env` et `*_file`. Les quatre champs sont `databases[n].password`, `destinations[n].access_key_id`, `destinations[n].secret_access_key` et `alerts.channels[n].smtp.password`. Un `*_file` est lu en retirant le retour à la ligne final. | `E-033`, § 5.1 `F1.2` | `secret_test.go › TestCFG02EverySensitiveFieldAcceptsItsThreeForms` |
| CFG-03 | Deux formes du même champ déclarées ensemble sont une **erreur** qui nomme les deux clés : koffr n'en choisit jamais une en silence. Un `*_file` absent ou un `*_env` non défini échoue **au démarrage**, pas au premier usage. | `E-033`, `E-115` | `secret_test.go › TestCFG03TwoFormsOfTheSameFieldIsAnError`, `› TestCFG03AMissingSecretFileFailsAtStartUp`, `› TestCFG03AnUnsetEnvironmentVariableFailsAtStartUp` |
| CFG-04 | `agent.timezone` est **obligatoire** et validé par `time.LoadLocation`. La variable `TZ` du système n'a aucun effet : elle ne remplace pas une déclaration manquante et ne prime pas sur celle qui est écrite. La base de fuseaux est **embarquée** dans le binaire. | `E-036`, § 5.1 `F1.5`, `N-6` | `timezone_test.go › TestCFG04TimezoneIsRequiredAndValidated`, `› TestCFG04TheSystemTimezoneHasNoEffect`, `› TestCFG04ASetTZDoesNotExcuseAMissingDeclaration`, `› TestCFG04TheZoneDatabaseIsEmbedded` |
| CFG-05 | La **forme cible du § 5.1**, copiée du cahier des charges, est acceptée telle quelle, et chaque clé atterrit dans le champ que le reste de koffr ira lire — y compris les deux écritures de `tools` (`auto` et la stratégie explicite). | `E-037`, § 5.1 | `reference_test.go › TestCFG05TheTargetFormOfTheSpecificationIsAccepted` |
| CFG-06 | Une valeur sensible **ne s'imprime jamais** : ni par `%v`, `%+v`, `%#v` ou `%s`, ni par `slog`, ni par la sérialisation YAML de `config show`. Elle ne sort que par `Expose()`. Un champ sensible non renseigné n'apparaît pas du tout. | `E-115`, § 6 | `redact_test.go › TestCFG06AConfigurationNeverPrintsItsSecrets`, `› TestCFG06SlogNeverPrintsASecret`, `› TestCFG06ASecretIsReachableThroughExposeOnly`, `› TestCFG06RedactedRendersTheTopologyWithoutASecret` |

## Chemins et espace de travail

Les chemins sont une donnée de configuration : c'est `config` qui les porte, et `state` qui les
consomme. Un adaptateur n'a pas de `rules.md` (ADR-0010), donc les deux règles vivent ici même si
le second test s'exécute dans `internal/state`.

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CFG-07 | L'arborescence de `E-026` est la **valeur par défaut** : `/etc/koffr/koffr.yaml`, `/etc/koffr/recipients.txt`, `/var/lib/koffr/koffr.db`, `/var/lib/koffr/tools/`, `/var/lib/koffr/tmp/`, `/var/log/koffr/koffr.log`. `--config` et `--state-dir` la déplacent, et chaque chemin suit le répertoire dont il dépend. | `E-026`, § 4.3, ADR-0001, `N-11` | `config/paths_test.go › TestCFG07TheDefaultPathsAreTheOnesOfE026`, `› TestCFG07EveryPathFollowsTheDirectoryItIsOverriddenWith`, `cli/paths_test.go › TestCFG07TheFlagsDefaultToTheProductionPaths` |
| CFG-08 | `tmp/` est vidé **à l'ouverture de l'état**, pas à chaque commande : une commande tapée à la main pendant une sauvegarde ne détruit pas son espace de travail. Le répertoire lui-même, `tools/` et `koffr.db` survivent, et l'arborescence est créée si elle manque. | `E-026`, § 4.3, `N-10` | `state/paths_test.go › TestCFG08OpeningTheStateClearsTheWorkingSpace`, `› TestCFG08ClearingTheWorkingSpaceSparesEverythingElse`, `› TestCFG08OpeningCreatesTheLayoutOfE026`, `cli/paths_test.go › TestCFG08ACommandRunByHandDoesNotClearTheWorkingSpace` |

## Divergences avec le cahier des charges

- **`config show --redact` ne se désactive pas** (`N-15`). Le CDC ne décrit pas la commande ; le
  plan l'écrit avec un drapeau. koffr n'a **aucun chemin de code** qui imprime un secret :
  `--redact=false` est refusé avec un message qui le dit. Annoncé à la recette.
- **`internal/config/testdata/reference.yaml` est le § 5.1 copié, avec `keeper` renommé en
  `koffr`** (ADR-0001). Aucune clé ne diffère : seul le nom du produit change dans les valeurs.
- **`config validate` et `config show` ne résolvent aucun secret** : ils lisent la forme. Lire la
  topologie d'un parc ne doit pas exiger les mots de passe de ce parc (`E-115`), et vérifier une
  configuration depuis un poste ne doit pas exiger les fichiers de production. `Load` résout ;
  `Parse` non.

## Non porté

- **`E-034`** — `config validate` ne vérifie **ni la joignabilité des bases ni l'existence des
  outils**. C'est le lot 1 : cela suppose les sondes et le résolveur. Le lot 0 ne valide que la
  forme (plan du lot 0, § Périmètre).
- **`F1.4`** — relecture à chaud sur `SIGHUP` et sur changement de `mtime`. Classée *souhaitable*
  par le CDC, renvoyée au lot 5, et le § 4.3 la contredit (`Q-12`).
- **`Q-04`** (destinataires de chiffrement par base) et **`Q-06`** (identifiants de restauration
  distincts) ajouteront des clés à ce schéma. En ajout, sans rupture (`N-7`).

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Chemin de la configuration | `/etc/koffr/koffr.yaml` | `E-026`, ADR-0001 | Défaut, surchargé par `--config` (`N-11`) |
| Répertoire d'état | `/var/lib/koffr` | `E-026`, ADR-0001 | Défaut, surchargé par `--state-dir` (`N-11`) |
| Marqueur de masquage | `[redacted]` | `N-15` | `redact_test.go` |
| Répertoire des journaux | `/var/log/koffr` | `E-026`, `E-121` | `paths_test.go` |

## Ce que `state` garantit, et qui n'est pas une règle de `config`

`internal/state` est un adaptateur : ses invariants sont ceux d'ADR-0006, vérifiés par ses propres
tests et par le schéma lui-même — tables `STRICT`, statuts contraints par `CHECK`, horodatages
RFC 3339 **UTC** imposés par un `GLOB`, tailles en octets et durées en millisecondes en entiers,
`journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL` posés sur
**chaque** connexion du pool. Voir `internal/state/migrations/0001_initial.sql` et
`internal/state/*_test.go`.
