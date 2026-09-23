# Règles — module `catalog` (code `CAT`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module **indexe**. Il ne sauvegarde pas, ne vérifie pas, n'écrit sur aucune destination. Et il
n'est **jamais la source de vérité** : ce qu'une restauration exige vit dans le manifeste déposé à
côté de l'archive, pour qu'un dépôt reste exploitable quand l'agent, son disque et son SQLite ont
disparu.

## L'instantané de configuration

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CAT-01 | Le catalogue garde un **instantané de la configuration résolue** de chaque base, avec son empreinte, pour qu'un changement se remarque. Deux configurations identiques donnent la **même** empreinte ; tout changement qui compte — hôte, port, utilisateur, moteur, destinations, tampon, planning, conteneur — la change. L'instantané ne porte **aucun identifiant de connexion**, et les champs émis sont **énumérés** : en ajouter un est une décision, pas une édition. | `E-028`, `E-115`, § 4.4 | `catalog/fingerprint_test.go › TestCAT01TheSameConfigurationGivesTheSameFingerprint`, `› TestCAT01AnyChangeThatMattersChangesTheFingerprint`, `› TestCAT01TheSnapshotCarriesNoCredential` |

## Le catalogue est un index

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| CAT-02 | **Tout ce qu'une restauration exige survit dans le manifeste**, sans le catalogue : identifiant, base, moment, tailles, empreintes, état de vérification, et ce qui dit **comment ouvrir** l'archive — format, chaîne de traitement, outil et sa version, destinataires. Le test le prouve de la seule façon qui vaille : il construit le manifeste, jette le catalogue, et reconstruit l'entrée depuis le manifeste seul. L'état de `P4` voyage avec : une archive que personne n'a vérifiée ne revendique rien. | ADR-0006, `E-059`, `E-058` | `catalog/manifest_test.go › TestCAT02EverythingARestoreNeedsSurvivesInTheManifest`, `› TestCAT02TheVerificationStateTravelsWithTheArchive` |

## Ce que le catalogue écrit

Écrit par `internal/state` sur la base SQLite d'`E-028` ; les règles ci-dessus sont pures, ce qui
suit est vérifié sur une **vraie** base.

| Constat | Tenu par |
| --- | --- |
| Enregistrer une base deux fois la **met à jour**, ne la refuse pas — une configuration est relue à chaque exécution | `state/catalog_test.go › TestADatabaseIsRecordedThenUpdated` |
| Une sauvegarde dont la base n'a jamais été enregistrée est **refusée par la clé étrangère**, pas par un contrôle en Go qu'on peut oublier d'écrire | `› TestABackupOfAnUnknownDatabaseIsRefused` |
| Une sauvegarde fraîchement écrite est `none` — `P4` : elle n'est pas vérifiée tant qu'elle ne l'est pas | `› TestABackupIsRecordedWithItsLocations` |
| Un état de vérification que le schéma ne connaît pas est refusé **à l'insertion** (ADR-0006) | `› TestAnUnknownVerificationStateIsRefused` |
| Vérifier une sauvegarde qui n'existe pas est une **erreur typée**, jamais un silence | `› TestSettingTheStateOfAnUnknownBackupIsAnError` |
| Le listing rend les plus récentes d'abord — un exploitant compare deux relevés | `› TestBackupsComeBackMostRecentFirst` |

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Empreinte de configuration | SHA-256 du JSON résolu | `E-028`, `CAT-01` | `catalog/fingerprint_test.go` |
| État initial d'une archive | `none` | § 2 `P4` | `state/catalog_test.go` |
