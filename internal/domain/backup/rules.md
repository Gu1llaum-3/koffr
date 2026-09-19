# Règles — module `backup` (code `BKP`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nnn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

Ce module orchestre une sauvegarde. Il ne dumpe pas — c'est `engine` —, il n'écrit pas — c'est
`store` —, il n'assemble pas le flux — c'est `pipeline`. Il décide **dans quel ordre**, **si on peut
commencer**, et **ce qu'on fait quand ça casse**.

## La chaîne en flux

Ces trois règles portent sur ce que `internal/pipeline` garantit. Elles vivent ici parce qu'un
adaptateur n'a pas de `rules.md` (ADR-0010) et qu'`ARCHITECTURE.md` ne déclare pas de code `PIP`
(`N-9`).

| # | Règle (une phrase, vérifiable) | Source | Test |
| --- | --- | --- | --- |
| BKP-07 | Le dump brut n'est **jamais matérialisé**, ni en mémoire ni sur disque : dump, compression, chiffrement et empreintes sont chaînés **en une seule passe**. Ce qui est retenu en mémoire reste borné quelle que soit la taille de la base. | `E-025`, § 4.5 `F3.3` | `pipeline/pipeline_test.go › TestBKP07TheStreamIsNeverHeldWhole` |
| BKP-08 | **Deux** empreintes sont calculées au vol : `sha256_raw` sur le dump **avant** compression, `sha256_stored` sur ce qui est réellement écrit. Seule la seconde se vérifie sans déchiffrer, et le lot 3 en dépend. | `E-024`, `N-7` | `pipeline/pipeline_test.go › TestBKP08BothChecksumsAreComputedOnTheWay` |
| BKP-09 | Une erreur **au milieu** du flux — dump interrompu, disque plein, destinataire refusé — remonte **typée** et ne produit **jamais** un résultat qui ressemble à un succès. Un octet écrit n'est pas une sauvegarde. | `E-024`, § 2 `P3` | `pipeline/pipeline_test.go › TestBKP09AFailureMidStreamIsNeverASuccess` |

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | --- | --- | --- |
| Niveau de compression | `zstd:3` | `N-4` (hypothèse de `Q-08`) | `pipeline/pipeline_test.go` |
