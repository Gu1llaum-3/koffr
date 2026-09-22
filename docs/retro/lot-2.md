# Rétrospective lot 2 — Sauvegarder une base vers un fichier chiffré

- **Période** : du 2026-09-19 au 2026-09-22, **12 vagues** (6 au plan initial, 6 au plan de
  corrections), 32 commits sur `main`.
- **Plans** : `docs/plans/lot-2-sauvegarde.md` et `docs/plans/lot-2-corrections.md`.
  **Critère de sortie** : **atteint**, les six points du plan de corrections compris.

## Ce qui s'est passé

C'est le premier lot où koffr **fait** quelque chose. Il dumpe, compresse, chiffre et écrit, et
l'archive produite s'ouvre **sans koffr** — vérifié par `age`, `zstd` et `pg_restore`, pas par nos
propres bibliothèques.

**26 règles métier** écrites : `CRY-01` à `CRY-05`, `BKP-01` à `BKP-21`, et `RSV-11` à `RSV-13`.
Chacune a son test ; 43 tests pour les seuls domaines `backup` et `crypto`.

**Quatre questions ouvertes depuis le démarrage du projet ont été tranchées** — `Q-01`, `Q-04`,
`Q-07`, `Q-08` — et figées par **ADR-0016**. Elles ne l'ont pas été par raisonnement mais sur des
mesures prises pendant la recette, sur une base PostgreSQL de 372 Mo et une MariaDB de 12 Mo. Une
de ces mesures a renversé une hypothèse : koffr supposait qu'une archive pèse 25 % du dump brut, la
réalité était **7,8 % et 3,9 %**. Le diviseur d'estimation est passé de 4 à 8.

**La recette a trouvé sept anomalies, dont trois bloquantes**, et toutes les trois étaient
invisibles depuis le poste de développement : toute la sortie texte partait sur la sortie d'erreur,
un job tué laissait son tampon pour toujours, et le journal ne disait rien de ce qu'une sauvegarde
avait fait. Elles ont demandé un plan de corrections de six vagues.

**Un défaut a été trouvé par un test de bout en bout et par rien d'autre** : `pg_dump -Fc` compresse
lui-même, et le manifeste du § 5.3 porte `--compress=0`, qui avait été manqué. zstd recompressait
donc du compressé, et les deux tailles du manifeste ne voulaient plus rien dire. Aucun test unitaire
de la vague 4 ne pouvait le voir : il fallait une base assez grosse pour que le rapport saute aux
yeux.

## Ce qui a marché

- **L'inconnue en premier.** La vague 1 a prouvé `E-075` — une archive s'ouvre avec le binaire `age`
  standard — **avant** que quoi que ce soit ne soit construit par-dessus. Si elle avait échoué, tout
  le lot changeait de forme ; elle a réussi, et le reste s'est appuyé dessus sans arrière-pensée.
- **Les gardes qui lisent les sources.** Le dépôt en compte trois : les requêtes SQL de
  `internal/engine`, les `cmd.Print*` de `internal/cli`, les noms de faits du journal. Chacune est
  née d'un défaut qu'aucune règle de lint ne savait exprimer, et chacune a **mordu** ensuite — la
  garde SQL a refusé trois requêtes avant qu'elles ne soient inscrites délibérément.
- **Mesurer plutôt qu'affirmer.** Le contraste `stage` / `stream` a été **échantillonné** pendant
  les jobs — pic du tampon, chevauchement dump/envoi — et c'est ce tableau qui a décidé `Q-01`. Le
  coût de la dépendance ULID a été mesuré (+32,8 Kio) avant d'être accepté, pas après.
- **La recette sur un parc volumineux.** Une base de deux tables et trois lignes n'aurait rien
  montré : ni `--compress=0`, ni le scénario 11, ni le tampon de 13 Mo laissé par un `kill -9`.
- **Le marché « le test dépose, le script exécute ».** Employé quatre fois — `-race`, Docker, `age`,
  et maintenant le scénario 6 de bout en bout. Il permet de prouver avec des outils qui ne sont pas
  les nôtres sans violer `AR-07`.

## Ce qui a coûté

Une seule cause, et elle revient **six fois** : **un contrôle vert ne prouve rien tant qu'on n'a pas
vu ce qu'il refuse.**

| Où | Ce qui était vert et ne prouvait rien |
| --- | --- |
| Recette lot 0, parcours 4.5 | « le fichier ne contient aucune ligne de journal » — le fichier était **vide**. Déclaré passé pendant deux lots (`A-12`) |
| Lot 2, vague 2 | deux tests mesuraient le **taux de compression** au lieu de la mise en tampon : des zéros se compressent à rien |
| Corrections, vague 1 | le harnais poussait `stdout` et `stderr` dans **le même canal** : ils pouvaient revenir intervertis |
| Corrections, vague 3 | test et code écrits ensemble sur l'identifiant : **le rouge n'a pas été constaté** |
| Corrections, vague 4 | `BKP-21` vérifiait qu'un secret **que le domaine ne reçoit jamais** n'apparaît pas |
| Corrections, vague 6 | le `kill -9` frappait à 2 s un job qui durait 1,5 s : rien à purger, donc rien de prouvé |

Deux autres coûts, de la même famille :

- **`A-17` : une décision de plan promise et jamais livrée.** `N-2` du plan du lot 2 écrivait que le
  champ de destinataires par base serait « ajouté au schéma **dès ce lot**, même vide ». Il ne l'a
  pas été, et **rien ne le testait**, donc rien ne l'a signalé. Une `N-n` sans tâche de test est une
  intention.
- **Trois pièges de plateforme découverts en CI ou sur l'instance**, pas en local :
  `Statfs_t.Bsize` change de type entre Linux et macOS, `lumberjack` purge ses archives depuis une
  goroutine qui survit à `Close`, et la CI ne se déclenche que sur la PR — attendre le run avant de
  créer la PR bloque indéfiniment.

## Ce qu'on change

| Quoi | Où | Fait |
| --- | --- | --- |
| Un test vert ne compte que si on a **vu ce qu'il refuse** : constater le rouge, ou, quand la précédence l'empêche, rendre la condition fausse une fois et le vérifier | `METHODE.md` § Exécution d'un plan |[x] |
| Un test d'**absence** (« X n'apparaît pas ») est remplacé par une **liste blanche** de ce qui est permis : l'absence passe pour de mauvaises raisons | skill `implementer` § TDD |[x] |
| Un test d'**interruption** doit interrompre : vérifier que l'état intermédiaire **existait** avant de vérifier qu'il a disparu | skill `implementer` § TDD |[x] |
| Un **harnais** qui compare deux flux ne doit pas pouvoir les intervertir ; un harnais est du code qui se relit | skill `implementer` § TDD |[x] |
| Une décision de plan `N-n` qui contraint le code **nomme la tâche qui la teste**, sinon elle n'est pas livrée | `METHODE.md` § Exécution d'un plan |[x] |
| Un scénario de recette **répercute les décisions** dans « ce qui est voulu et pourrait passer pour un bug », sinon le rejeu produit de fausses anomalies | `docs/recette/README.md` |[x] |
| `pg_dump -Fc` **compresse lui-même** : `--compress=0` ou le pipeline compresse du compressé | `CLAUDE.md` § Conventions |[x] |
| `cmd.Print*` de cobra écrit sur la sortie **d'erreur** quand aucun écrivain n'est posé — jamais en test, toujours dans le binaire | `CLAUDE.md` § Conventions |[x] |
| La CI ne se déclenche que sur la **PR** : attendre le run avant d'ouvrir la PR bloque | `CLAUDE.md` § Git |[x] |
| La politique de sauvegarde — tampon, destinataires, portée, estimation | ADR-0016 | [x] |
| `CLAUDE.md` dépasse 300 lignes, le seuil de relecture est 150 | `B-07`, à trancher | [ ] |

## Pour le kit

- **La règle la plus rentable de ce lot n'est pas technique** : *avant de cocher, faire échouer le
  test une fois*. Elle aurait attrapé cinq des six cas ci-dessus, et le sixième — le fichier vide de
  la recette — appelle la même discipline côté scénario : **un parcours de recette doit dire ce
  qu'on doit voir, pas seulement ce qu'on ne doit pas voir.**
- **Le motif « le test dépose, le script exécute »** est réutilisable partout où il faut prouver
  quelque chose avec un outil externe sans le laisser entrer dans le code : le test écrit les
  pièces dans un répertoire donné par un drapeau, un script lance le binaire réel, et son absence
  est un saut bruyant devenu échec en CI par une variable d'environnement.
- **Une garde qui lit les sources** est la réponse quand une règle ne s'exprime ni dans un import ni
  dans un lint : liste blanche, message qui dit quoi faire, et le fait qu'y ajouter une entrée est
  une **décision**, pas une édition.
