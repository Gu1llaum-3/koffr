# Rétrospective lot 1 — Diagnostic d'un parc réel

- **Période** : du 2026-09-18 au 2026-09-19, 9 vagues (6 au plan initial, 3 au plan de
  corrections), 36 commits.
- **Plans** : `docs/plans/lot-1-diagnostic.md` et `docs/plans/lot-1-corrections.md`.
  **Critère de sortie** : **atteint**, avec deux points **amendés en route par ADR-0014**.

## Ce qui s'est passé

Le lot devait répondre à une question : *sur ce parc, quel outil sauvegardera quelle base, et
comment le sait-on ?* Il y répond, et c'est vérifié sur un parc réel de trois familles.

Deux décisions ont changé le lot en cours de route, et les deux venaient du propriétaire.

**`D-06` a été tranchée, et l'installation gérée est sortie du MVP** (ADR-0014). La vague 6, qui
devait la livrer, est devenue celle qui assume proprement son absence. `E-043`, `E-044`, `E-045` et
`E-131` sont reportées ; `Q-15` est sans objet. Ce qui devait bloquer le lot l'a en réalité
simplifié.

**La recette a trouvé trois anomalies bloquantes**, toutes liées, et toutes invisibles depuis le
poste de développement. Elles ont demandé un plan de corrections de trois vagues.

Un fait mérite d'être noté à part : **`E-041` a été vérifiée sur un cas que la distribution a
fabriqué elle-même**. Après un conflit de paquets, `/usr/bin/mysqldump` était celui de MariaDB ;
koffr a refusé de l'utiliser pour la base MySQL. Ce n'est pas un cas construit pour le test.

## Ce qui a marché

- **Déclarer le port avant d'écrire l'adaptateur.** La vague 1 a posé `ServerProbe` ; la vague 3 —
  le cœur du produit, la matrice de compatibilité et le piège du § 5.2 — s'est écrite et testée
  **sans une seule base qui tourne**. C'est ce qui a rendu la partie la plus délicate du lot la plus
  rapide.
- **Une garde là où le lint ne sait pas exprimer la règle.** ADR-0013 a ouvert `database/sql` à
  `internal/engine` ; `queries_test.go` lit les littéraux SQL du paquet et refuse tout ce qui n'est
  pas `SELECT VERSION()`. Une règle assouplie a reçu sa contrepartie le jour même.
- **Le parc de recette comme oracle.** Trois familles réelles ont montré ce qu'aucun simulacre
  n'aurait montré : un wrapper Debian qui dispatche sur `argv[0]`, et deux paquets clients qui
  s'excluent.
- **Reporter une exigence plutôt que la prétendre tenue.** `E-042` est inscrite *partiellement
  couverte*, avec sa raison. Le registre dit la vérité, y compris quand elle est inconfortable.

## Ce qui a coûté

- **Deux merges avec une CI rouge.** La première fois, un `echo` placé avant le `&&` du merge
  effaçait le code de retour qu'on croyait tester ; la seconde, la commande de surveillance avait
  couru avant que l'exécution n'existe. *Coût* : `main` cassé deux fois, un correctif en urgence,
  et une PR qui n'aurait jamais dû être mergée.
- **Un correctif écrit sans le reproduire d'abord.** `A-09` a résisté à une première correction qui
  passait sur le poste et échouait sur l'instance : le message d'erreur du wrapper contient le mot
  `postgresql`, parce que **le chemin du wrapper le contient**. *Coût* : une vague à reprendre, et
  un aller-retour de plus avec la machine de recette. *Cause* : le test portait une approximation du
  message, pas le message exact que la machine avait produit.
- **Une implémentation écrasée par `git checkout` sur un fichier jamais indexé.** Pour prouver
  qu'une garde mordait, le fichier a été modifié puis « restauré » — vers une version antérieure.
  Deux mécanismes l'ont masqué : le test concerné était **sauté** faute de Docker dans ce shell, et
  le cache de `go test` répondait `(cached)` dans l'autre. *Coût* : une CI rouge, un commit de
  réparation. *Cause* : `git checkout` restaure depuis l'index, pas depuis l'état courant.
- **Un ordre d'itération non déterministe livré jusqu'en CI.** `Diagnose` parcourait une `map`,
  donc `config validate` rendait un premier échec différent à chaque appel. *Coût* : un merge
  fautif, puis un correctif. *Cause* : aucune règle ne disait que la sortie d'une commande doit être
  ordonnée.
- **Une écriture de plan échouée en silence.** Un script d'édition a levé une exception après avoir
  modifié sa copie mémoire : le fichier n'a **rien** reçu, alors que le commit partait. *Coût* :
  faible, rattrapé au commit suivant. *Cause* : le code de retour du commit ne dit rien du contenu
  du fichier.

## Ce qu'on change

| Quoi | Où | Fait |
| --- | --- | --- |
| Un correctif se **reproduit** avant d'être écrit, et le test porte le **message exact** de la machine, jamais une approximation | `METHODE.md` § Exécution d'un plan | [x] |
| Ce qu'une commande affiche est **ordonné** : deux exécutions identiques donnent la même sortie, et un parcours de `map` n'en est jamais la source | `CLAUDE.md` § Conventions | [x] |
| Après avoir modifié un fichier par script, **relire le fichier**, pas seulement le code de retour | skill `implementer` § Avant de dire « terminé » | [x] |
| Les pièges de la pile du lot 1 : `pg_wrapper` et l'`argv[0]`, les clients MySQL et MariaDB qui s'excluent | `CLAUDE.md` § Conventions | [x] |
| Une règle de lint assouplie s'accompagne de la garde qui la remplace | skill `implementer` § TDD | [x] |
| Le parc mixte et la stratégie `exec` | ADR-0015 | [x] |
| L'installation gérée hors MVP | ADR-0014 | [x] |
| `internal/engine` peut sonder | ADR-0013 | [x] |

## Pour le kit

1. **Le gabarit de recette devrait demander que le test d'un correctif porte la sortie exacte de la
   machine**, collée telle quelle. Deux fois dans ce lot, une approximation a laissé passer le
   défaut.
2. **La Definition of done devrait exiger qu'une sortie de commande soit ordonnée.** C'est une
   propriété qu'on ne pense à vérifier qu'après l'avoir perdue, et elle se teste en deux lignes.
3. **Le skill d'exécution devrait interdire un merge qui ne soit pas enchaîné directement sur la
   commande d'attente** — leçon déjà tirée au lot 0, et enfreinte deux fois ici : elle mérite d'être
   dans le kit, pas seulement dans ce projet.
