package subprovider

import (
	"testing"
)

func TestDetectPortugueseVariant(t *testing.T) {
	tests := []struct {
		name           string
		content        []byte
		minSample      int
		wantVariant    PortugueseVariant
		wantConfidence float64
	}{
		{
			name:           "clear Portuguese-BR content",
			content:        []byte("Você precisa pegar o ônibus na fila. Meu celular está carregando na geladeira. Comi um sorvete legal com o garoto. Nosso time ganhou o trem."),
			minSample:      3,
			wantVariant:    VariantBR,
			wantConfidence: 1.0,
		},
		{
			name:           "clear Portuguese-PT content",
			content:        []byte("Tu sabes onde fica o autocarro? O telemóvel não tem sinal. Apanhei o comboio para ir ao frigorífico. Comprei um gelado fixe para o miúdo. A equipa ganhou a bicha."),
			minSample:      3,
			wantVariant:    VariantPT,
			wantConfidence: 1.0,
		},
		{
			name:           "ambiguous mixed vocabulary - pt wins",
			content:        []byte("Você é fixe e eu sou miúdo. O autocarro do ônibus chegou. Telemóvel e celular estão carregados."),
			minSample:      3,
			wantVariant:    VariantPT,
			wantConfidence: 4.0 / 7.0,
		},
		{
			name:           "empty content",
			content:        []byte(""),
			minSample:      1,
			wantVariant:    VariantUnknown,
			wantConfidence: 0,
		},
		{
			name:           "single BR marker, too few samples",
			content:        []byte("Celular"),
			minSample:      3,
			wantVariant:    VariantUnknown,
			wantConfidence: 0,
		},
		{
			name:           "single BR marker, enough samples",
			content:        []byte("Celular é muito legal no Brasil"),
			minSample:      1,
			wantVariant:    VariantBR,
			wantConfidence: 1.0,
		},
		{
			name:           "strong BR with one PT marker",
			content:        []byte("Você e o miúdo pegaram o ônibus. Celular na geladeira. Sorvete na fila. Garoto no time. Trem legal."),
			minSample:      3,
			wantVariant:    VariantBR,
			wantConfidence: 10.0 / 11.0,
		},
		{
			name:           "strong PT with one BR marker",
			content:        []byte("Tu e você apanharam o autocarro. Telemóvel sem carga. Comboio para o frigorífico. Gelado fixe. Equipa do rapaz."),
			minSample:      3,
			wantVariant:    VariantPT,
			wantConfidence: 9.0 / 10.0,
		},
		{
			name:           "non-Portuguese content - no markers",
			content:        []byte("The quick brown fox jumps over the lazy dog. Hello world foo bar baz."),
			minSample:      1,
			wantVariant:    VariantUnknown,
			wantConfidence: 0,
		},
		{
			name:           "portuguese but no regional markers",
			content:        []byte("Eu gosto muito de comer e dormir. O dia está bonito e o sol brilha forte."),
			minSample:      1,
			wantVariant:    VariantUnknown,
			wantConfidence: 0,
		},
		{
			name:           "Tu at start of sentences (marker fix verification)",
			content:        []byte("Tu viste isso? Tu não sabias. Vós viajaste de autocarro. O telemóvel não tinha bateria. O comboio saiu atrasado. Frigorífico cheio. Gelado derretido. Bicha no supermercado. Casa de banho suja. Rapaz simpático. Fixe demais. Miúdo na escola. Equipa venceu."),
			minSample:      3,
			wantVariant:    VariantPT,
			wantConfidence: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVariant, gotConfidence := DetectPortugueseVariant(tt.content, tt.minSample)
			if gotVariant != tt.wantVariant {
				t.Errorf("DetectPortugueseVariant() variant = %q, want %q", gotVariant, tt.wantVariant)
			}
			if gotConfidence != tt.wantConfidence {
				t.Errorf("DetectPortugueseVariant() confidence = %f, want %f", gotConfidence, tt.wantConfidence)
			}
		})
	}
}
