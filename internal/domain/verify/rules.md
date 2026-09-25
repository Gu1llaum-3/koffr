# Règles — module `verify` (code `VRF`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module dit **ce qui vaut comme vérifié**. Il ne lit pas les archives — c'est `store` —, il ne
lance pas d'outil — c'est `engine`. Il décide ce qui compte, et `P4` en dépend : rien n'est
sauvegardé tant que ce n'est pas vérifié.

## La structure d'un dump

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| VRF-01 | La structure d'un dump PostgreSQL est contrôlée par **`pg_restore --list`**, sur le flux qui passe : une table des matières cohérente vaut structure saine. Un flux qui n'est **pas** un dump — octets aléatoires, texte, vide — est **refusé**. C'est le contrôle qu'une empreinte ne sait pas faire : un `pg_dump` qui échoue après deux octets produit une archive parfaitement cohérente de rien. | `E-063`, `E-014`, § 5.4 `F4.2` | `engine/structure_test.go › TestVRF01ARealPostgreSQLDumpHasACoherentTableOfContents`, `› TestVRF01AFlowThatIsNotADumpIsRefused` |
| VRF-02 | Pour MySQL et MariaDB, la structure est le **marqueur de fin** `-- Dump completed`, cherché **à la fin** du flux et nulle part ailleurs — une ligne de données qui le cite n'est pas un dump terminé. Un dump coupé avant sa dernière ligne est **refusé** : c'est l'échec qu'une empreinte ne voit pas, l'archive étant intacte et le dump incomplet. | `E-063`, § 5.4 `F4.2` | `engine/structure_test.go › TestVRF02ARealMariaDBDumpCarriesItsEndMarker`, `› TestVRF02ADumpCutBeforeItsMarkerIsRefused`, `› TestVRF02TheMarkerIsLookedForAtTheEnd` |

**Trois états, jamais deux.** Un contrôle que koffr **n'a pas pu mener** n'est ni un succès ni un
échec de l'archive : `P4` veut qu'« non vérifié » et « vérifié et mauvais » ne se confondent jamais.
`verify.Structure` porte donc `Checked` **et** `OK`.

## L'empreinte

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| VRF-03 | L'empreinte est **recalculée en relisant la destination**, jamais depuis ce que la chaîne a retenu en mémoire — sans quoi koffr ne prouverait que son accord avec lui-même. Une archive corrompue **après** son écriture fait échouer le job. Une archive qui ne passe pas ses contrôles n'est **pas** une sauvegarde : `P4`. | `E-062`, `E-008`, § 5.4 `F4.1` | `backup/service_test.go › TestVRF03TheChecksumIsRecomputedFromTheDestination`, `› TestVRF03ASoundArchivePassesItsChecksum`, `› TestAnArchiveWhoseStructureIsRefusedFailsTheJob` |

**Trois états, encore.** Un contrôle impossible — pas de `pg_restore` sur la machine, destination
illisible — rend `Checked` faux, et **ne passe jamais** pour un succès. Le catalogue l'enregistre
alors `none` et non `failed` : « personne n'a regardé » et « on a regardé, c'est mauvais » sont deux
situations différentes pour un exploitant.

## Ce qu'un exploitant voit

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| VRF-04 | Une archive **vérifiée**, une archive **en échec** et une archive que **personne n'a regardée** se lisent différemment dans `koffr list`, et l'absence de vérification n'est **jamais** rendue comme un succès : elle dit **non**, en toutes lettres, plutôt que de laisser un blanc qu'un œil fatigué lit comme « ça va ». | `E-064`, § 5.4 `F4.3`, § 2 `P4` | `cli/list_test.go › TestVRF04VerifiedAndUnverifiedAreVisuallyDistinct` |
| VRF-05 | `koffr verify` relit l'archive **depuis sa destination**, recalcule l'empreinte, met le catalogue à jour — et **dit ce qu'il ne fait pas** : il ne rejoue pas la structure, parce que la clé privée qui ouvrirait l'archive n'est pas sur la machine. Un exploitant qui croirait que tout a été revérifié se tromperait. Une archive disparue ou modifiée passe en `failed` : c'est exactement ce à quoi sert une vérification. | `E-103c`, `E-062`, ADR-0017 | `cli/verify_test.go › TestVerifyRecomputesTheChecksumAndSaysWhatItCannotDo`, `› TestVerifyFailsOnAnArchiveThatChanged`, `› TestVerifyReportsAnArchiveThatIsNoLongerThere`, `› TestVerifyNamesTheArchivesItKnows` |

## Divergences avec le cahier des charges

- **`F4.2` demande de relire la structure de l'archive *écrite* ; koffr la contrôle *au vol*,
  pendant le dump.** Raison : l'archive est chiffrée et koffr ne détient qu'une clé publique
  (`E-113`, ADR-0007) ; le seul instant où il voit le dump en clair est pendant qu'il le produit.
  Figé par **ADR-0017**, qui porte aussi ce que la décision coûte : la structure est vérifiée **une
  fois**, à l'écriture, et jamais rejouée — une corruption survenue ensuite est vue par l'empreinte,
  pas par elle.
- **Conséquence pour `E-065`** (revérification périodique des vieilles archives, lot 6) : elle ne
  pourra porter que sur l'**empreinte**, pour la même raison. À dire quand ce lot sera planifié.

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Queue conservée pour le marqueur | 4 Kio | `N-2` du plan du lot 3 | `engine/structure_test.go` |
| Table des matières conservée | 64 Kio | `N-2` | `engine/structure_test.go` |
| Octets retenus par le contrôle PostgreSQL | **0** — le flux est transmis, jamais gardé | `E-025` | `› TestWatchingDoesNotConsumeTheStream` |
