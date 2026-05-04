# CRISPR-LM: Toward Unified Surgical Editing of Language Models via Automated Diagnosis, Localization, and Intervention

**Caleb Gross**

*Draft hypothesis paper -- v0.1, April 2026*

---

## Abstract

The ability to make targeted post-hoc modifications to language model behavior has advanced rapidly along independent fronts: causal tracing enables localization of factual associations in MLP layers (Meng et al., 2022), directional ablation removes behavioral patterns by projecting out activation directions (Arditi et al., 2024), and representation engineering identifies linear subspaces corresponding to high-level concepts (Zou et al., 2023). However, these techniques remain siloed -- each addresses a narrow class of model defects using a distinct methodology, and none provides a mechanism for diagnosing which technique is appropriate for a given defect. We propose CRISPR-LM, a framework that unifies surgical model editing behind a diagnose-locate-edit pipeline. Inspired by the CRISPR-Cas9 system in molecular biology, CRISPR-LM combines (1) an automated diagnostic layer that classifies failure types and estimates their localizability, (2) a taxonomy of five edit types ordered by information-theoretic cost, and (3) a verification protocol that measures both target efficacy and off-target regression. Before constructing the framework, we propose a foundational localizability study across five failure categories and three model families to test the premise that model defects are sufficiently concentrated in identifiable parameter subsets to permit surgical correction. We outline the experimental protocol, success criteria, and the conditions under which the premise would be refuted.

## 1. Introduction

Language models exhibit a range of failure modes -- factual hallucination, format non-compliance, detail omission, undesirable behavioral patterns, and capability gaps. When these failures are discovered after training, practitioners face a coarse set of corrective tools: retraining on augmented data, full fine-tuning, or replacement of the model entirely. Each of these approaches modifies the model globally, risking collateral damage to capabilities unrelated to the target defect.

A parallel body of work has demonstrated that certain model behaviors can be modified with far greater precision. Meng et al. (2022) showed that individual factual associations are causally mediated by specific MLP layers and can be rewritten via rank-one weight updates. Arditi et al. (2024) demonstrated that refusal behavior is mediated by a single direction in activation space and can be removed by orthogonalizing weight matrices with respect to that direction. Turner et al. (2023) showed that adding a fixed vector to intermediate activations can steer model behavior without any weight modification. These results suggest that at least some model behaviors are localized enough to permit surgical intervention.

However, the current landscape is fragmented along three axes:

**Diagnosis is absent.** Existing methods assume the practitioner already knows what kind of defect they are addressing and which technique to apply. There is no automated system that takes a failure case -- an (input, expected output, actual output) triple -- and determines what type of defect it represents, where in the model the defect originates, or which corrective technique is appropriate.

**Edit types are siloed.** Factual editing (Meng et al., 2022; 2023), behavioral ablation (Arditi et al., 2024), steering vectors (Turner et al., 2023), adapter injection (Houlsby et al., 2019; Hu et al., 2022), and targeted fine-tuning operate on different assumptions about defect structure and use incompatible methodologies. Gupta et al. (2024) proposed a unified preservation-memorization objective for direct weight-editing methods (ROME and MEMIT specifically), but this unification does not extend across paradigms -- it does not address behavioral ablation, adapter-based approaches, or inference-time interventions.

**Verification is inconsistent.** Individual editing methods typically evaluate target efficacy (did the edit fix the intended behavior?) but vary widely in how -- or whether -- they measure off-target effects. There is no standard protocol for measuring the collateral impact of an edit across the model's broader capability surface.

We propose CRISPR-LM as a framework to address these three gaps. Drawing on the analogy to CRISPR-Cas9 gene editing, we structure the framework around three stages: a diagnostic layer (guide RNA) that identifies and localizes defects, an edit selector that chooses the minimum-cost intervention from a taxonomy of five edit types (repair template), and a verification protocol that measures both target efficacy and off-target regression (off-target effects screening).

Critically, the entire framework rests on a testable premise: that model defects are sufficiently localized to permit targeted intervention. Recent work has challenged the simplicity of early localization results -- Geva et al. (2023) showed that factual recall involves a multi-stage pipeline of MLP enrichment, relation propagation, and attribute extraction across layers, while Wei et al. (2024) demonstrated that relational knowledge is encoded in attention mechanisms as well as MLPs, complicating the picture presented by purely MLP-focused analyses. We therefore propose a foundational localizability study as a prerequisite to framework construction, and define explicit criteria under which the premise would be considered refuted.

## 2. Related Work

### 2.1 Localization of Knowledge and Behavior in Transformers

The question of where knowledge resides in transformer parameters has been investigated from several angles. Geva et al. (2021) demonstrated that feed-forward layers operate as key-value memories, with lower layers capturing shallow patterns and upper layers capturing semantic content. Dai et al. (2022) introduced the concept of knowledge neurons and showed that specific neurons express specific relational facts. Meng et al. (2022) developed causal tracing -- a method that corrupts subject token representations and measures the effect of restoring individual layer activations -- and found that mid-layer MLP modules are decisive for factual recall.

Subsequent work has complicated this picture. Geva et al. (2023) dissected the recall mechanism into three stages: early MLP layers enrich the subject representation with attributes, middle layers propagate relational information, and late attention heads extract the target attribute. This distributed pipeline suggests that even "localized" facts involve coordinated computation across multiple components. Yao et al. (2024) mapped full knowledge circuits -- subgraphs of attention heads and MLP layers that collaboratively produce factual outputs -- and showed that editing methods like ROME affect these circuits in ways that extend beyond the targeted layer.

Wei et al. (2024) directly questioned the localization hypothesis, finding that entity knowledge and relational knowledge are stored through different mechanisms, with relational knowledge significantly encoded in attention modules rather than solely in MLPs. This finding suggests that the localizability of a defect may depend on its type -- an observation central to our proposed localizability study.

### 2.2 Model Editing Methods

**Locate-and-edit.** ROME (Meng et al., 2022) uses causal tracing to identify the decisive MLP layer for a factual association, then applies a rank-one update to the layer's value projection to overwrite the association. MEMIT (Meng et al., 2023) extends this to batch editing across multiple layers. Gupta et al. (2024) unified ROME and MEMIT under a shared preservation-memorization objective, introducing EMMET as a generalization, though this unification remains within the locate-and-edit paradigm.

**Behavioral ablation.** Arditi et al. (2024) demonstrated that the refusal direction in language models can be identified via the difference in mean activations between harmful and harmless prompts, and removed by orthogonalizing weight matrices against this direction. This finding has been operationalized in open-source tools (p-e-w, 2024; elder-plinius, 2024) and applied to a range of behavioral directions beyond refusal.

**Steering vectors.** Turner et al. (2023) showed that adding a fixed vector to residual stream activations at a specific layer can shift model behavior at inference time -- e.g., increasing or decreasing sycophancy, agreeableness, or specific topical tendencies. This approach requires no weight modification and is fully reversible.

**Representation engineering.** Zou et al. (2023) proposed a systematic approach to identifying linear representations of high-level concepts (honesty, harmfulness, fairness) in activation space, enabling both reading and controlling model behavior through these representations.

**Parameter-efficient adaptation.** Houlsby et al. (2019) introduced adapter layers -- small bottleneck modules inserted between transformer layers and trained while the base model is frozen. Hu et al. (2022) proposed LoRA, which injects low-rank decompositions into weight matrices rather than inserting new modules. These methods add new capacity rather than editing existing parameters, making them suited to capability gaps rather than localized defects.

**Interpretability tools.** Bricken et al. (2023) and Templeton et al. (2024) developed sparse autoencoders that decompose model activations into interpretable features, providing a higher-resolution view of the representation space than directional methods. These tools serve as potential diagnostic components but have not been integrated into editing pipelines.

### 2.3 Frameworks and Surveys

Wang et al. (2024) provide a comprehensive survey of knowledge editing methods, categorizing approaches into memory-based, meta-learning, and locate-then-edit families. The EasyEdit toolkit (Wang et al., 2024) consolidates multiple editing methods into a single codebase, supporting ROME, MEMIT, MEND, SERAC, IKE, and dynamic LoRA. However, EasyEdit is an engineering toolkit -- the user must select the method manually. It does not provide automated diagnosis, cost-based method selection, or cross-method verification.

To our knowledge, no existing work combines automated failure diagnosis, cross-paradigm method selection, and unified verification into a single pipeline. This is the gap CRISPR-LM addresses.

## 3. The CRISPR-LM Framework

### 3.1 Overview

CRISPR-LM is a three-stage pipeline:

1. **Diagnosis.** Given a model and a failure case (input, expected output, actual output), classify the failure type, estimate the causal concentration of the defect, and determine whether it is amenable to surgical editing.

2. **Intervention.** Select and apply the minimum-cost edit type from a taxonomy ordered by information-theoretic cost, subject to the constraint that the defect's localizability exceeds the method's minimum threshold.

3. **Verification.** Measure target efficacy (did the edit resolve the failure case and similar cases?) and off-target regression (did the edit degrade other capabilities beyond a defined tolerance?).

### 3.2 Diagnostic Layer

The diagnostic layer operates at two tiers:

**Tier B (fast path).** A lightweight classifier maps failure cases to known failure types and retrieves pre-computed localization priors from the results of the foundational study (Section 4). For a known failure type on a studied model architecture, this tier provides an immediate estimate of which components to target and which edit type to attempt. The classifier operates on features extracted from the (input, expected, actual) triple and incurs negligible computational cost.

**Tier A (deep scan).** When Tier B confidence is below a threshold, or when encountering a failure type not present in the taxonomy, the system performs full causal tracing: corrupting the input representation and systematically restoring activations at each layer, attention head, and MLP block to measure their causal contribution to the failure. Optionally, sparse autoencoder decomposition can identify individual features responsible for the defect. This tier is computationally expensive -- on the order of minutes for small models and hours for large models -- but produces a complete causal responsibility map.

The two tiers form a feedback loop: every Deep Scan result that produces a successful edit updates the Fast Classifier's priors, improving its coverage over time.

### 3.3 Causal Concentration Index

We introduce the **Causal Concentration Index (CCI)** as a quantitative measure of defect localizability. For a given failure case, let $c_i$ denote the causal effect of component $i$ (where a component is an attention head or MLP block), measured by the change in output probability of the correct token when that component's clean activation is restored to a corrupted forward pass. The CCI at rank $k$ is:

$$\text{CCI}@k = \frac{\sum_{i=1}^{k} c_{(i)}}{\sum_{i=1}^{N} c_{(i)}}$$

where $c_{(i)}$ denotes the $i$-th largest causal effect and $N$ is the total number of components. CCI@k measures the fraction of total causal effect captured by the $k$ most responsible components. A CCI@5 of 0.8 means five components account for 80% of the causal effect -- highly localized. A CCI@5 of 0.2 means the effect is spread across many components -- diffuse.

We note that CCI is related to but distinct from existing localization measures in the literature. Meng et al. (2022) used causal tracing heatmaps qualitatively; CCI provides a single scalar that enables quantitative comparison across failure types, models, and components.

### 3.4 Edit Taxonomy

We define five edit types, ordered by their information-theoretic cost -- loosely, the number of bits modified in the model's parameter space:

**Type 1: Steering vectors.** Add a direction vector to activations at a specified layer during inference. Cost: zero weight modification (inference-time only). Reversible: yes. Applicable when the defect corresponds to a behavioral direction that can be amplified or suppressed.

**Type 2: Directional ablation.** Project out a direction from weight matrices across specified layers. Cost: rank-1 modification per layer. Reversible: in principle (store the projected component). Applicable when the defect is a behavioral pattern mediated by a single activation direction.

**Type 3: Factual rewrite.** Apply a rank-one update to the value projection of a specific MLP layer, following the ROME methodology. Cost: rank-1 modification to a single layer. Reversible: yes (store the original weights or the rank-1 delta). Applicable when the defect is a specific factual association localized to identifiable MLP layers.

**Type 4: Targeted micro-finetune.** Freeze all model parameters except those in the layers identified by the diagnostic layer, and fine-tune the unfrozen layers on a small corrective dataset (10-100 examples). Cost: gradient-based updates to a subset of layers. Reversible: only by checkpoint restoration. Applicable when the defect is localized but too structurally complex for a rank-1 correction.

**Type 5: Spoke injection.** Add new adapter parameters at each decoder layer and train them on task-specific data, following the approach of adapter methods (Houlsby et al., 2019) or spoke architectures. Cost: new parameters added to the model. Reversible: yes (remove the adapter). Applicable when the defect represents a capability gap that cannot be addressed by editing existing parameters.

### 3.5 Escalation and PAM Check

Before attempting an edit, the framework verifies that the defect's CCI meets the minimum threshold for the selected edit type. We term this the PAM check, by analogy to the protospacer adjacent motif required for CRISPR-Cas9 binding. Edit types 1-3 (low-cost, rank-1 or inference-time) require CCI@5 above 0.5. Type 4 (micro-finetune) requires CCI@5 above 0.3. Type 5 (spoke injection) has no CCI requirement, as it adds new capacity rather than editing existing parameters.

If the recommended edit fails verification (Section 3.6), the framework escalates to the next edit type in the cost ordering. If all edit types fail, the defect is declared non-editable -- an honest outcome that is reported rather than masked.

### 3.6 Verification Protocol

Every edit is evaluated on two dimensions:

**Target efficacy.** The original failure cases, plus a held-out set of similar cases, are re-evaluated on the edited model. For factual edits, we use exact-match accuracy. For behavioral edits, we measure the directional shift on a Likert-scale evaluation (e.g., sycophancy reduction measured across a calibrated prompt set).

**Off-target regression.** We measure (1) perplexity change on a held-out general corpus, with a configurable regression budget (e.g., maximum 0.5% perplexity increase, calibrated per model family), (2) KL divergence between pre-edit and post-edit output distributions on a neutral prompt set, and (3) cross-category spot checks to ensure that fixing one failure type does not introduce another.

Each completed edit produces a structured record containing the diagnosis, intervention parameters, target efficacy metrics, and regression measurements, enabling full traceability and reproducibility.

## 4. Proposed Evaluation: The Localizability Study

The entire framework rests on the premise that model defects are sufficiently concentrated in identifiable parameter subsets to permit surgical correction. Before constructing the framework, we propose to test this premise directly.

### 4.1 Failure Categories

We define five failure categories spanning a range of expected localizability:

1. **Factual hallucination.** The model produces incorrect factual claims (e.g., incorrect dates, attributes, or associations). Prior work suggests high localization in mid-layer MLPs. *Expected CCI: high.*

2. **Format violation.** The model ignores structural output constraints (e.g., producing prose when JSON is requested, or violating a schema). Hypothesized to involve final-layer attention patterns and output projection. *Expected CCI: medium-high.*

3. **Detail omission.** The model drops specific values -- line numbers, exact quantities, proper nouns -- during summarization or compression tasks. *Localization unknown; this category tests whether the framework extends beyond previously studied failure types.*

4. **Behavioral pattern.** The model exhibits undesirable patterns such as sycophancy, excessive hedging, or refusal. Prior work (Arditi et al., 2024) has shown these patterns are mediated by identifiable directions in activation space. *Expected CCI: moderate (distributed but low-rank).*

5. **Capability gap.** The model fails at a task it was not trained for (e.g., a base model asked to follow instructions). This category serves as a **negative control** -- we expect low CCI, and if it shows high CCI, our methodology may be flawed.

### 4.2 Models

We evaluate across three open-weight model families at different scales:

- Qwen 2.5 2B
- Llama 3.1 8B
- Gemma (appropriate available variant)

Cross-family evaluation tests whether localizability patterns generalize or are architecture-specific. The selection balances practical constraints (local execution on consumer hardware) with architectural diversity.

### 4.3 Protocol

For each (failure category, model) pair:

1. Curate 50 failure cases as (input, expected output, actual output) triples, ensuring diversity within the category.
2. Run causal tracing: for each failure case, corrupt the input, then systematically restore activations at each component (attention head and MLP block at each layer) and measure the change in output probability of the expected token(s).
3. Compute CCI@5 and CCI@10 for each failure case. Report the distribution (median, interquartile range) across the 50 cases.
4. Produce a localizability heatmap showing causal effect by component and layer, aggregated across failure cases.

### 4.4 Success Criteria

- **Proceed with framework construction** if at least 3 of 5 failure categories show median CCI@5 > 0.5 on at least 2 of 3 model families.
- **Pivot to partial framework** (steering vectors and ablation only) if 2 failure categories meet the threshold.
- **Publish as a negative result** if fewer than 2 failure categories meet the threshold. A rigorous demonstration that model defects are too diffuse for surgical editing would itself be a contribution.
- **Methodology check:** Category 5 (capability gap) should show low CCI. If it does not, the causal tracing methodology may be measuring something other than defect localization.

### 4.5 Anticipated Challenges

We note several challenges with the proposed study:

**Causal tracing was designed for factual associations.** Extending it to behavioral and structural failure types (categories 2-4) may require methodological adaptation. The "correct token" is well-defined for factual recall but less clear for format violations or behavioral patterns, where the desired change is distributional rather than token-level.

**Model scale affects localization.** Larger models may distribute computation differently than smaller ones. Results on 2B and 8B models may not transfer to 70B+ models. We acknowledge this limitation and propose that the framework's edit-type selection be re-calibrated for new model scales.

**Circuit-level vs. component-level localization.** Yao et al. (2024) showed that knowledge is encoded in circuits -- coordinated subgraphs of components -- not isolated components. CCI measures component-level concentration and may underestimate localizability if the relevant components form a sparse but distributed circuit. We plan to report both component-level CCI and circuit-level analysis where feasible.

## 5. Expected Contributions

If the localizability study supports the premise, CRISPR-LM would contribute:

1. **A quantitative localizability taxonomy.** The first systematic measurement of causal concentration across failure types and model families, extending localization analysis beyond factual associations to behavioral and structural defects.

2. **The Causal Concentration Index.** A scalar metric for defect localizability that enables quantitative comparison across defect types, models, and editing methods.

3. **A cross-paradigm editing framework.** The first system that combines automated diagnosis, cost-ordered edit selection across five paradigms (steering, ablation, locate-and-edit, targeted fine-tuning, and adapter injection), and unified verification.

4. **An escalation principle.** A cost-ordered intervention strategy grounded in the information-theoretic cost of each edit type, providing a principled answer to the question "which editing technique should I try first?"

5. **Composition analysis.** An investigation of how multiple surgical edits interact when applied to the same model -- whether they compose, conflict, or degrade.

If the localizability study refutes the premise, the contribution is the refutation itself: a rigorous demonstration of the limits of surgical model editing, with quantitative evidence for which failure types are and are not amenable to targeted intervention.

## 6. Discussion

**Relationship to CRISPR-Cas9.** The biological analogy is structural, not mechanistic. CRISPR-Cas9 operates on a discrete symbolic substrate (DNA nucleotides) with well-understood base-pairing rules. Neural network parameters are continuous and their relationship to behavior is mediated by distributed computation. The analogy is useful for framing the pipeline (diagnose, locate, edit, verify) but should not be taken to imply that model editing can achieve the precision of gene editing. In particular, the "off-target effects" problem in both domains may prove to be the binding constraint.

**Scope.** This paper proposes a framework and evaluation plan; it does not present results. The localizability study is a prerequisite that may refute the premise. We consider this intellectual honesty, not a limitation -- the question of whether surgical model editing generalizes across failure types is itself open, and answering it rigorously is valuable regardless of the direction.

**Limitations of CCI.** The Causal Concentration Index is a coarse measure. It captures whether causal effect is concentrated but does not capture whether the concentrated components form an editable structure. A defect could be highly concentrated (high CCI) but resistant to rank-1 modification if the causal mechanism is nonlinear. The PAM check and escalation protocol are designed to handle this case, but the gap between "localizable" and "editable" warrants further investigation.

**Ethical considerations.** Surgical model editing is a dual-use capability. The same framework that corrects factual hallucinations can inject factual errors. The same pipeline that removes undesirable behaviors can remove safety-relevant behaviors. We note that existing tools (p-e-w, 2024; elder-plinius, 2024) already enable behavioral ablation at scale. CRISPR-LM's contribution is the diagnostic and verification layers, which if anything make the edit process more transparent and auditable. Nonetheless, we acknowledge the dual-use concern and advocate for responsible disclosure practices.

## References

[1] Arditi, A., Obeso, O., Syed, A., Paleka, D., Panickssery, N., Gurnee, W., and Nanda, N. "Refusal in Language Models Is Mediated by a Single Direction." arXiv preprint arXiv:2406.11717, 2024.

[2] Bricken, T., Templeton, A., Batson, J., Chen, B., Jermyn, A., Conerly, T., Turner, N., Anil, C., Denison, C., Askell, A., Lasenby, R., Wu, Y., Kravec, S., Schiefer, N., Maxwell, T., Joseph, N., Hatfield-Dodds, Z., Tamkin, A., Nguyen, K., McLean, B., Burke, J.E., Hume, T., Carter, S., Henighan, T., and Olah, C. "Towards Monosemanticity: Decomposing Language Models With Dictionary Learning." Transformer Circuits Thread, 2023.

[3] Dai, D., Dong, L., Hao, Y., Sui, Z., Chang, B., and Wei, F. "Knowledge Neurons in Pretrained Transformers." In Proceedings of the 60th Annual Meeting of the Association for Computational Linguistics (ACL), 2022.

[4] Geva, M., Schuster, R., Berant, J., and Levy, O. "Transformer Feed-Forward Layers Are Key-Value Memories." In Proceedings of the Conference on Empirical Methods in Natural Language Processing (EMNLP), 2021.

[5] Geva, M., Bastings, J., Filippova, K., and Globerson, A. "Dissecting Recall of Factual Associations in Auto-Regressive Language Models." In Proceedings of the Conference on Empirical Methods in Natural Language Processing (EMNLP), 2023.

[6] Gupta, A., Sajnani, D., and Anumanchipalli, G. "A Unified Framework for Model Editing." In Findings of the Association for Computational Linguistics: EMNLP, 2024.

[7] Houlsby, N., Giurgiu, A., Jastrzebski, S., Morrone, B., de Laroussilhe, Q., Gesmundo, A., Attariyan, M., and Gelly, S. "Parameter-Efficient Transfer Learning for NLP." In Proceedings of the 36th International Conference on Machine Learning (ICML), 2019.

[8] Hu, E.J., Shen, Y., Wallis, P., Allen-Zhu, Z., Li, Y., Wang, S., Wang, L., and Chen, W. "LoRA: Low-Rank Adaptation of Large Language Models." In Proceedings of the International Conference on Learning Representations (ICLR), 2022.

[9] Meng, K., Bau, D., Andonian, A., and Belinkov, Y. "Locating and Editing Factual Associations in GPT." In Advances in Neural Information Processing Systems (NeurIPS), 2022.

[10] Meng, K., Sharma, A.S., Andonian, A., Belinkov, Y., and Bau, D. "Mass-Editing Memory in a Transformer." In Proceedings of the International Conference on Learning Representations (ICLR), 2023.

[11] Templeton, A., Conerly, T., Marcus, J., Lindsey, J., Bricken, T., Chen, B., Pearce, A., Citro, C., Ameisen, E., Jones, A., Cunningham, H., Turner, N.L., McDougall, C., MacDiarmid, M., Tamkin, A., Durmus, E., Hume, T., Mosconi, F., Freeman, C.D., Sumers, T.R., Rees, E., Batson, J., Jermyn, A., Carter, S., Olah, C., and Henighan, T. "Scaling Monosemanticity: Extracting Interpretable Features from Claude 3 Sonnet." Transformer Circuits Thread, 2024.

[12] Turner, A.M., Thiergart, L., Leech, G., Udell, D., Vazquez, J.J., Mini, U., and MacDiarmid, M. "Steering Language Models With Activation Engineering." arXiv preprint arXiv:2308.10248, 2023.

[13] Wang, S., Zhu, Y., Liu, H., Zheng, Z., Chen, C., and Li, J. "Knowledge Editing for Large Language Models: A Survey." ACM Computing Surveys, Vol. 57, 2024.

[14] Wei, Y., Yu, X., Weng, Y., Ma, H., Zhang, Y., Zhao, J., and Liu, K. "Does Knowledge Localization Hold True? Surprising Differences Between Entity and Relation Perspectives in Language Models." In Proceedings of the ACM International Conference on Information and Knowledge Management (CIKM), 2024.

[15] Yao, Y., Zhang, N., Xi, Z., Wang, M., Xu, Z., Deng, S., and Chen, H. "Knowledge Circuits in Pretrained Transformers." In Advances in Neural Information Processing Systems (NeurIPS), 2024.

[16] Wang, P., Zhang, N., Tian, B., Xi, Z., Yao, Y., Xu, Z., Wang, M., Mao, S., Wang, X., Cheng, S., Liu, K., Ni, Y., Zheng, G., and Chen, H. "EasyEdit: An Easy-to-use Knowledge Editing Framework for Large Language Models." In Proceedings of the 62nd Annual Meeting of the Association for Computational Linguistics, Volume 3: System Demonstrations (ACL), pp. 82-93, 2024.

[17] Zou, A., Phan, L., Chen, S., Campbell, J., Guo, P., Ren, R., Pan, A., Yin, X., Mazeika, M., Dombrowski, A.-K., Goel, S., Li, N., Byun, M.J., Wang, Z., Mallen, A., Basart, S., Koyejo, S., Song, D., Fredrikson, M., Kolter, J.Z., and Hendrycks, D. "Representation Engineering: A Top-Down Approach to AI Transparency." arXiv preprint arXiv:2310.01405, 2023.

[18] p-e-w. "Heretic: Fully automatic LLM censorship removal." GitHub repository, 2024. https://github.com/p-e-w/heretic

[19] elder-plinius. "OBLITERATUS." GitHub repository, 2024. https://github.com/elder-plinius/OBLITERATUS
