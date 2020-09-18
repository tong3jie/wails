#ifndef __FFENESTRI_H__
#define __FFENESTRI_H__

#include <stdio.h>

extern void *NewApplication(const char *title, int width, int height, int resizable, int devtools, int fullscreen, int startHidden);
extern void SetMinWindowSize(void *app, int minWidth, int minHeight);
extern void SetMaxWindowSize(void *app, int maxWidth, int maxHeight);
extern void Run(void *app, int argc, char **argv);
extern void DestroyApplication(void *app);
extern void SetDebug(void *app, int flag);
extern void SetBindings(void *app, const char *bindings);
extern void ExecJS(void *app, const char *script);
extern void Hide(void *app);
extern void Show(void *app);
extern void Center(void *app);
extern void Maximise(void *app);
extern void Unmaximise(void *app);
extern void ToggleMaximise(void *app);
extern void Minimise(void *app);
extern void Unminimise(void *app);
extern void ToggleMinimise(void *app);
extern void SetSize(void *app, int width, int height);
extern void SetPosition(void *app, int x, int y);
extern void Quit(void *app);
extern void SetTitle(void *app, const char *title);
extern void Fullscreen(void *app);
extern void UnFullscreen(void *app);
extern void ToggleFullscreen(void *app);
extern int SetColour(void *app, const char *colourString);
extern void DisableFrame(void *app);
extern char *SaveFileDialog(void *appPointer, char *title, char *filter);
extern char *OpenFileDialog(void *appPointer, char *title, char *filter);
extern char *OpenDirectoryDialog(void *appPointer, char *title, char *filter);

#endif