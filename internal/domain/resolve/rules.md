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
| RSV-02 | La version **et la famille** d'un candidat sont obtenues **en l'exécutant**, jamais en interprétant son chemin ou son nom : un binaire nommé `pg_dump-16` qui répond `15.4` est en 15.4, et un `mysqldump` qui répond `…-MariaDB` appartient à MariaDB. Un candidat qu'on ne peut pas exécuter, qui répond n'importe quoi ou qui ne répond pas à temps est **écarté**, pas deviné. | `E-007`, `E-039`, `E-041`, § 5.2 `F2.2` | `engine/discover_test.go › TestTheVersionComesFromRunningTheToolNotFromItsName`, `› TestTheFamilyOfAToolComesFromRunningIt`, `› TestACandidateThatCannotAnswerIsDropped` |
| RSV-03 | Le résultat est mis en cache pour la durée du processus, et **invalidé au changement de `mtime`** du binaire : une seconde interrogation n'exécute rien, une installation qui remplace l'outil est vue. | `E-039`, `N-4` | `engine/discover_test.go › TestTheSecondLookRunsNothing`, `› TestAChangedBinaryIsLookedAtAgain` |

## Divergences avec le cahier des charges

- **`PATH` est interrogé par `exec.LookPath`, pas en lisant la variable.** `AR-05` réserve la
  lecture de l'environnement à `internal/config`, et `LookPath` rend ce qui s'exécuterait
  réellement. Conséquence assumée : si `PATH` contient deux versions du même outil, seule la
  première est vue par cette source — les autres le sont par les chemins système.

## Non porté

- **La matrice de compatibilité et le choix** (`E-040`, `E-042`, `E-047` à `E-050`) : vague 3.
- **L'installation gérée** (`E-043`, `E-044`, `E-045`, `E-131`) : vague 6, bloquée par `D-06` et
  `Q-15`. L'énumération regarde déjà `/var/lib/koffr/tools/`, qui est simplement vide.
- **La stratégie `exec`** (`E-046`) : vague 5. La provenance `container` existe dans le modèle et
  n'est encore produite par personne.

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Délai d'exécution d'un candidat | 5 s | `N-10` du plan du lot 1 | `discover_test.go` |
| Répertoire des outils gérés | `/var/lib/koffr/tools` | `E-026`, `CFG-07` | `config/paths_test.go` |
