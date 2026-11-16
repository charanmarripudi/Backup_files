#include <SDL2/SDL.h>
#include <stdio.h>

int main() {
    if (SDL_Init(SDL_INIT_VIDEO) < 0) {
        printf("SDL_Init failed: %s\n", SDL_GetError());
        return 1;
    }
    
    int num = SDL_GetNumVideoDrivers();
    printf("Available video drivers:\n");
    for (int i = 0; i < num; i++) {
        printf("  %s\n", SDL_GetVideoDriver(i));
    }
    
    SDL_Quit();
    return 0;
}
