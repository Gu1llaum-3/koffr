# Règles — module `resolve` (code `RSV`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module décide **quel outil sauvegardera quelle base**. C'est le cœur technique du produit et,
dit le § 5.2, « la principale source de bugs silencieux dans les outils concurrents ». Son principe
tient en une phrase : **la compatibilité prime sur la provenance.**

## Énumération des candidats

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| RSV-01 | L'énumération visite **toutes** les sources — chemins système connus par distribution, `PATH`, répertoire géré, sortie de `pg_lsclusters` — et n'exprime **aucune préférence** : l'ordre dans lequel elle rend ses candidats ne porte pas de sens. | `E-013`, `E-038`, § 5.2 `F2.1` | `engine/discover_test.go › TestDiscoveryVisitsEverySource` |
| RSV-02 | La version **et la famille** d'un candidat sont obtenues **en l'exécutant**, jamais en interprétant son chemin ou son nom : un binaire nommé `mysqldump` qui répond `…-MariaDB` appartient à MariaDB. Le candidat est exécuté **par le chemin où il a été trouvé** — un lien résolu retire à un wrapper l'`argv[0]` dont il vit. Une réponse qui ne **nomme ni l'outil ni sa famille** est écartée, **quel que soit le nombre qu'elle contient** : c'est ainsi que `at … line 153` était devenu une version 153.0. Un candidat qu'on ne peut pas exécuter ou qui ne répond pas à temps est écarté, jamais deviné. | `E-007`, `E-039`, `E-041`, § 5.2 `F2.2`, `A-09`, `N-1` et `N-2` du plan de corrections | `engine/discover_test.go › TestTheVersionComesFromRunningTheToolNotFromItsName`, `› TestTheFamilyOfAToolComesFromRunningIt`, `› TestACandidateThatCannotAnswerIsDropped`, `› TestACandidateThatNamesNothingIsDropped`, `› TestTheRealAnswersAreStillAccepted`, `› TestAToolIsRunByThePathItWasFoundAt`, `› TestTwoPathsToTheSameBinaryAreOneCandidate` |
| RSV-03 | Le résultat est mis en cache pour la durée du processus, et **invalidé au changement de `mtime`** du binaire : une seconde interrogation n'exécute rien, une installation qui remplace l'outil est vue. | `E-039`, `N-4` | `engine/discover_test.go › TestTheSecondLookRunsNothing`, `› TestAChangedBinaryIsLookedAtAgain` |

## Compatibilité et choix

La matrice du § 5.2, et la phrase qui la gouverne : **la compatibilité prime sur la provenance.**

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| RSV-04 | PostgreSQL, sauvegarde : un candidat convient si `major(pg_dump) ≥ major(serveur)`. Un `pg_dump` plus ancien que le serveur est **écarté**, jamais utilisé « pour voir ». | `E-047`, § 5.2 matrice | `matrix_test.go › TestRSV04PostgreSQLDumpNeedsAToolAtLeastAsRecent` |
| RSV-05 | PostgreSQL, restauration : un candidat convient si `major(pg_restore) ≥ major(archive)`. | `E-048`, § 5.2 matrice | `matrix_test.go › TestRSV05PostgreSQLRestoreNeedsAToolAtLeastAsRecent` |
| RSV-06 | PostgreSQL, cible de restauration : `major(cible) ≥ major(origine)` est **conseillé** ; une restauration descendante est **signalée et jamais bloquée**. | `E-049`, § 5.2 matrice | `matrix_test.go › TestRSV06ADownwardRestoreIsWarnedAboutNotBlocked` |
| RSV-07 | MySQL et MariaDB : un candidat convient si la **famille est identique** et si `version(client) ≥ version(serveur)`. Un outil de l'autre famille est écarté **même s'il est le seul disponible**. | `E-050`, `E-041`, § 5.2 matrice | `matrix_test.go › TestRSV07TheMySQLFamiliesAreNeverCrossed` |
| RSV-08 | Parmi les candidats **compatibles**, c'est la version **la plus proche par le haut** qui est retenue. La provenance — hôte, puis géré, puis conteneur — ne départage que des candidats de version égale. C'est le « piège à éviter » du § 5.2, nommé par le CDC parce que les outils concurrents y tombent. | `E-040`, § 5.2 `F2.3` | `matrix_test.go › TestRSV08TheClosestCompatibleVersionWins`, `› TestRSV08ProvenanceOnlyBreaksTies` |
| RSV-09 | Sans candidat compatible, la résolution échoue avec un message qui nomme **la version attendue**, **les versions trouvées** et **la commande exacte** pour corriger. Aucun repli sur un outil incompatible n'existe : le texte fait partie de la règle. | `E-007`, `E-042`, § 5.2 `F2.5` | `matrix_test.go › TestRSV09AnImpossibleResolutionSaysWhatToDo` |

## Stratégie `exec`

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| RSV-10 | La stratégie `exec` se déclare **par base**, jamais globalement, et exige le **socket Docker** : sans lui, l'échec **nomme le socket attendu** plutôt que de se rabattre en silence sur un outil de l'hôte. Une base qui déclare `strategy: exec` **sans `container`** est refusée **à la validation de la configuration**, pas au premier job. L'outil trouvé dans le conteneur a la provenance `container`, et sa version est obtenue **en l'exécutant là**. | `E-013`, `E-046`, § 5.2 `F2.9` | `engine/exec_test.go › TestRSV10TheContainerToolIsFoundAndRunThere`, `› TestRSV10WithoutADockerSocketTheFailureNamesIt`, `config/exec_test.go › TestRSV10ExecWithoutAContainerIsRefusedAtValidation` |

## Divergences avec le cahier des charges

- **`PATH` est interrogé par `exec.LookPath`, pas en lisant la variable.** `AR-05` réserve la
  lecture de l'environnement à `internal/config`, et `LookPath` rend ce qui s'exécuterait
  réellement. Conséquence assumée : si `PATH` contient deux versions du même outil, seule la
  première est vue par cette source — les autres le sont par les chemins système.

## Non porté

- **L'installation gérée** (`E-043`, `E-044`, `E-045`, `E-131`) : vague 6, bloquée par `D-06` et
  `Q-15`. L'énumération regarde déjà `/var/lib/koffr/tools/`, qui est simplement vide.

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Délai d'exécution d'un candidat | 5 s | `N-10` du plan du lot 1 | `discover_test.go` |
| Répertoire des outils gérés | `/var/lib/koffr/tools` | `E-026`, `CFG-07` | `config/paths_test.go` |
